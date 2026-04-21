package prd

import (
	"fmt"
	"strings"
)

// FieldError describes a single validation failure keyed by a JSON path
// (e.g. "userStories[2].id"). The Path is stable and consumed by the SPA
// to surface the error next to the offending cell.
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Validate walks the PRD and returns every field-level error. An empty
// slice means the PRD is valid. Type-level mismatches (e.g. priority not
// an integer) are caught earlier by json.Unmarshal against the typed PRD
// struct and never reach this function.
func Validate(p *PRD) []FieldError {
	var errs []FieldError
	if p == nil {
		return []FieldError{{Path: "", Message: "prd is nil"}}
	}
	if strings.TrimSpace(p.Project) == "" {
		errs = append(errs, FieldError{Path: "project", Message: "project is required"})
	}

	idIndex := make(map[string]int)
	for i, s := range p.UserStories {
		base := fmt.Sprintf("userStories[%d]", i)
		if strings.TrimSpace(s.ID) == "" {
			errs = append(errs, FieldError{Path: base + ".id", Message: "id is required"})
		} else if prev, dup := idIndex[s.ID]; dup {
			errs = append(errs, FieldError{
				Path:    base + ".id",
				Message: fmt.Sprintf("duplicate id %q (also at userStories[%d].id)", s.ID, prev),
			})
		} else {
			idIndex[s.ID] = i
		}
		if strings.TrimSpace(s.Title) == "" {
			errs = append(errs, FieldError{Path: base + ".title", Message: "title is required"})
		}
		if strings.TrimSpace(s.Description) == "" {
			errs = append(errs, FieldError{Path: base + ".description", Message: "description is required"})
		}
		if s.Priority < 0 {
			errs = append(errs, FieldError{Path: base + ".priority", Message: "priority must be zero or greater"})
		}
		for k, ac := range s.AcceptanceCriteria {
			if strings.TrimSpace(ac) == "" {
				errs = append(errs, FieldError{
					Path:    fmt.Sprintf("%s.acceptanceCriteria[%d]", base, k),
					Message: "acceptance criterion cannot be empty",
				})
			}
		}
	}

	// dependsOn checks run in a second pass so self- and forward-references
	// resolve against the fully-indexed id set.
	for i, s := range p.UserStories {
		base := fmt.Sprintf("userStories[%d]", i)
		for k, dep := range s.DependsOn {
			depPath := fmt.Sprintf("%s.dependsOn[%d]", base, k)
			if strings.TrimSpace(dep) == "" {
				errs = append(errs, FieldError{Path: depPath, Message: "dependency id cannot be empty"})
				continue
			}
			if dep == s.ID {
				errs = append(errs, FieldError{Path: depPath, Message: "story cannot depend on itself"})
				continue
			}
			if _, ok := idIndex[dep]; !ok {
				errs = append(errs, FieldError{
					Path:    depPath,
					Message: fmt.Sprintf("unknown story id %q", dep),
				})
			}
		}
	}

	return errs
}
