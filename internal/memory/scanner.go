package memory

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"github.com/eoghanhynes/ralph/internal/debuglog"
	"os"
	"path/filepath"
	"strings"
)

// skipDirs contains directory names to skip during codebase scanning.
var skipDirs = map[string]bool{
	"vendor":       true,
	"node_modules": true,
	".git":         true,
	".ralph":       true,
	".jj":          true,
	"testdata":     true,
	"dist":         true,
	"build":        true,
	".next":        true,
	"__pycache__":  true,
}

// maxFileSize is the maximum file size (100KB) to consider for scanning.
const maxFileSize = 100 * 1024

// ScanCodebase walks the project directory, extracts summaries from source and
// config files, and embeds them into the ralph_codebase collection. It skips
// files that haven't changed since the last scan (by comparing file mod time).
func ScanCodebase(ctx context.Context, projectDir string, client *ChromaClient, embedder Embedder) error {
	files, err := collectFiles(projectDir)
	if err != nil {
		return fmt.Errorf("collect files: %w", err)
	}
	debuglog.Log("[memory/scanner] found %d files to consider in %s", len(files), projectDir)

	// Get existing documents to check mod times for incremental scanning.
	existing := make(map[string]float64) // file_path -> stored mod time (unix)
	docs, err := getAllDocuments(ctx, client, CollectionCodebase.Name)
	if err == nil {
		for _, doc := range docs {
			if fp, ok := doc.Metadata["file_path"].(string); ok {
				if mt, ok := doc.Metadata["file_mod_time"].(float64); ok {
					existing[fp] = mt
				}
			}
		}
	}

	var toEmbed []fileEntry
	skipped := 0
	for _, f := range files {
		relPath := f.relPath
		storedMod, found := existing[relPath]
		if found && storedMod >= float64(f.modTime) {
			skipped++
			continue
		}
		toEmbed = append(toEmbed, f)
	}
	debuglog.Log("[memory/scanner] files to embed: %d, skipped (unchanged): %d", len(toEmbed), skipped)

	if len(toEmbed) == 0 {
		return nil
	}

	// Build summaries and embed in batches.
	const batchSize = 20
	embedded := 0
	for i := 0; i < len(toEmbed); i += batchSize {
		end := i + batchSize
		if end > len(toEmbed) {
			end = len(toEmbed)
		}
		batch := toEmbed[i:end]

		summaries := make([]string, len(batch))
		docMetas := make([]fileMeta, len(batch))
		for j, f := range batch {
			summary, meta := summarizeFile(f)
			summaries[j] = summary
			docMetas[j] = meta
		}

		embeddings, err := embedder.Embed(ctx, summaries)
		if err != nil {
			return fmt.Errorf("embed batch %d-%d: %w", i, end-1, err)
		}

		upsertDocs := make([]Document, len(batch))
		for j, f := range batch {
			docID := fileDocID(f.relPath)
			upsertDocs[j] = Document{
				ID:        docID,
				Content:   summaries[j],
				Embedding: embeddings[j],
				Metadata: map[string]interface{}{
					"file_path":        f.relPath,
					"package_name":     docMetas[j].packageName,
					"exported_symbols": docMetas[j].exportedSymbols,
					"file_mod_time":    float64(f.modTime),
				},
			}
		}

		if err := client.UpsertDocuments(ctx, CollectionCodebase.Name, upsertDocs); err != nil {
			return fmt.Errorf("upsert batch %d-%d: %w", i, end-1, err)
		}
		embedded += len(batch)
	}

	debuglog.Log("[memory/scanner] embedded %d files into %s", embedded, CollectionCodebase.Name)
	return nil
}

// fileEntry holds info about a discovered file.
type fileEntry struct {
	absPath string
	relPath string
	modTime int64 // unix seconds
	isGo    bool
}

// fileMeta holds extracted metadata from a file summary.
type fileMeta struct {
	packageName     string
	exportedSymbols int
}

// collectFiles walks projectDir and returns scannable files.
func collectFiles(projectDir string) ([]fileEntry, error) {
	var files []fileEntry

	err := filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}

		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		if info.Size() > maxFileSize {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(info.Name()))
		name := strings.ToLower(info.Name())
		isGo := ext == ".go"
		isConfig := ext == ".yaml" || ext == ".yml" || ext == ".toml" || ext == ".json"
		isReadme := strings.HasPrefix(name, "readme")

		if !isGo && !isConfig && !isReadme {
			return nil
		}

		relPath, _ := filepath.Rel(projectDir, path)
		files = append(files, fileEntry{
			absPath: path,
			relPath: relPath,
			modTime: info.ModTime().Unix(),
			isGo:    isGo,
		})
		return nil
	})

	return files, err
}

// summarizeFile generates a text summary and metadata for a file.
func summarizeFile(f fileEntry) (string, fileMeta) {
	if f.isGo {
		return summarizeGoFile(f)
	}
	return summarizeNonGoFile(f)
}

// summarizeGoFile uses go/parser and go/ast to extract package, exported
// functions, exported types, and file-level comments.
func summarizeGoFile(f fileEntry) (string, fileMeta) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, f.absPath, nil, parser.ParseComments)
	if err != nil {
		// Fall back to non-Go summary if parsing fails.
		return summarizeNonGoFile(f)
	}

	var b strings.Builder
	meta := fileMeta{}

	// Package declaration.
	pkgName := node.Name.Name
	meta.packageName = pkgName
	fmt.Fprintf(&b, "package %s\n", pkgName)
	fmt.Fprintf(&b, "file: %s\n\n", f.relPath)

	// File-level doc comment.
	if node.Doc != nil {
		b.WriteString(node.Doc.Text())
		b.WriteString("\n")
	}

	// Exported types and functions.
	exported := 0
	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						exported++
						fmt.Fprintf(&b, "type %s %s\n", s.Name.Name, typeKind(s.Type))
					}
				}
			}
		case *ast.FuncDecl:
			if d.Name.IsExported() {
				exported++
				sig := funcSignature(d)
				b.WriteString(sig)
				b.WriteString("\n")
			}
		}
	}

	meta.exportedSymbols = exported
	return b.String(), meta
}

// typeKind returns a short description of the AST type node kind.
func typeKind(expr ast.Expr) string {
	switch expr.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	case *ast.ArrayType:
		return "array"
	case *ast.MapType:
		return "map"
	case *ast.FuncType:
		return "func"
	case *ast.ChanType:
		return "chan"
	default:
		return "alias"
	}
}

// funcSignature renders a Go function declaration as a signature string.
func funcSignature(d *ast.FuncDecl) string {
	var b strings.Builder
	b.WriteString("func ")

	// Method receiver.
	if d.Recv != nil && len(d.Recv.List) > 0 {
		r := d.Recv.List[0]
		b.WriteString("(")
		b.WriteString(exprString(r.Type))
		b.WriteString(") ")
	}

	b.WriteString(d.Name.Name)
	b.WriteString("(")

	// Parameters.
	if d.Type.Params != nil {
		var params []string
		for _, p := range d.Type.Params.List {
			typStr := exprString(p.Type)
			if len(p.Names) == 0 {
				params = append(params, typStr)
			} else {
				for _, n := range p.Names {
					params = append(params, n.Name+" "+typStr)
				}
			}
		}
		b.WriteString(strings.Join(params, ", "))
	}
	b.WriteString(")")

	// Results.
	if d.Type.Results != nil && len(d.Type.Results.List) > 0 {
		var results []string
		for _, r := range d.Type.Results.List {
			typStr := exprString(r.Type)
			if len(r.Names) == 0 {
				results = append(results, typStr)
			} else {
				for _, n := range r.Names {
					results = append(results, n.Name+" "+typStr)
				}
			}
		}
		if len(results) == 1 {
			b.WriteString(" " + results[0])
		} else {
			b.WriteString(" (" + strings.Join(results, ", ") + ")")
		}
	}

	return b.String()
}

// exprString returns a simplified string representation of an AST expression.
func exprString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	case *ast.StarExpr:
		return "*" + exprString(e.X)
	case *ast.ArrayType:
		return "[]" + exprString(e.Elt)
	case *ast.MapType:
		return "map[" + exprString(e.Key) + "]" + exprString(e.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.Ellipsis:
		return "..." + exprString(e.Elt)
	case *ast.FuncType:
		return "func(...)"
	case *ast.ChanType:
		return "chan " + exprString(e.Value)
	default:
		return "?"
	}
}

// summarizeNonGoFile reads the first 50 lines of a file as its summary.
func summarizeNonGoFile(f fileEntry) (string, fileMeta) {
	file, err := os.Open(f.absPath)
	if err != nil {
		return fmt.Sprintf("file: %s (unreadable)", f.relPath), fileMeta{}
	}
	defer file.Close()

	var b strings.Builder
	fmt.Fprintf(&b, "file: %s\n\n", f.relPath)

	scanner := bufio.NewScanner(file)
	lines := 0
	for scanner.Scan() && lines < 50 {
		b.WriteString(scanner.Text())
		b.WriteString("\n")
		lines++
	}

	return b.String(), fileMeta{}
}

// fileDocID generates a stable document ID from a file path.
func fileDocID(relPath string) string {
	h := sha256.Sum256([]byte(relPath))
	return fmt.Sprintf("codebase-%x", h[:8])
}
