package history

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
)

// Display-name generator: produces a deterministic, memorable two-word slug
// from a run_id so the UI has something friendlier than "run-1776477507618-
// osfy2l" to show. The slug is stored on Manifest.DisplayName at OpenRun
// time and never changes afterwards.
//
// Deterministic: the same run_id always maps to the same name, so a crashed
// daemon's half-written manifest keeps its identity when the UI recomputes.
// Collision-safe for practical purposes: ~50 adjectives × ~60 nouns =
// 3,000 combinations; same (repo, day) pair collisions are rare and
// disambiguated at render time by appending the last 4 chars of run_id.
//
// Future: swap in a small-LLM generator keyed by (repo, kind, prd-title)
// for more thematic names — keep the function signature stable so callers
// don't change.

// DisplayNameFor returns "<adj>-<noun>" seeded by the run_id hash.
func DisplayNameFor(runID string) string {
	if runID == "" {
		return ""
	}
	h := sha256.Sum256([]byte(runID))
	// Use the first 4 bytes for the adjective index, next 4 for the noun.
	adjIdx := binary.BigEndian.Uint32(h[0:4]) % uint32(len(nameAdjectives))
	nounIdx := binary.BigEndian.Uint32(h[4:8]) % uint32(len(nameNouns))
	return nameAdjectives[adjIdx] + "-" + nameNouns[nounIdx]
}

// lookup helpers so tests can introspect the pool without exposing the
// slices themselves.

func namePoolSize() (int, int) {
	return len(nameAdjectives), len(nameNouns)
}

// normaliseDisplayName strips whitespace and lowercases — used when we
// later accept LLM-generated names, to keep the on-disk shape consistent.
func normaliseDisplayName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Word lists curated for memorability + neutrality. No proper nouns, no
// jargon, all single-syllable-dominant for quick recall. Alphabetical for
// diff-ability; order is insignificant to the hash.

var nameAdjectives = []string{
	"amber", "azure", "boreal", "brisk", "calm", "clever", "cobalt",
	"copper", "crimson", "crisp", "dusk", "ember", "fleet", "forest",
	"gentle", "glacial", "golden", "humble", "iron", "jade", "keen",
	"lively", "lucid", "lush", "mellow", "mirth", "noble", "obsidian",
	"opal", "parallel", "placid", "quiet", "quick", "radiant", "raven",
	"russet", "sable", "scarlet", "serene", "silver", "slate", "solar",
	"steady", "swift", "twilight", "valiant", "velvet", "verdant",
	"vivid", "warm", "wintry",
}

var nameNouns = []string{
	"anchor", "arbor", "archer", "aspen", "badger", "beacon", "birch",
	"cedar", "cipher", "comet", "compass", "crane", "dawn", "delta",
	"ember", "falcon", "ferry", "forge", "gale", "glade", "harbor",
	"hawk", "heron", "horizon", "ivory", "journey", "kestrel", "lantern",
	"lark", "ledger", "meadow", "mercury", "nimbus", "orbit", "otter",
	"oxide", "phoenix", "prism", "quartz", "raven", "reed", "river",
	"sable", "saffron", "sonnet", "spark", "starling", "summit", "thicket",
	"thistle", "thrush", "tinder", "torrent", "vector", "vesper", "warden",
	"willow", "zephyr",
}

var _ = namePoolSize    // silence unused-export lint in packages without tests
var _ = normaliseDisplayName
