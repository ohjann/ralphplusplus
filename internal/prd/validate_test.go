package prd

import (
	"testing"
)

func findErr(errs []FieldError, path string) *FieldError {
	for i := range errs {
		if errs[i].Path == path {
			return &errs[i]
		}
	}
	return nil
}

func TestValidate_ValidPRD(t *testing.T) {
	p := &PRD{
		Project: "x",
		UserStories: []UserStory{
			{ID: "A-1", Title: "t", Description: "d", Priority: 0},
			{ID: "A-2", Title: "t", Description: "d", Priority: 1, DependsOn: []string{"A-1"}},
		},
	}
	if errs := Validate(p); len(errs) != 0 {
		t.Fatalf("expected no errors, got %+v", errs)
	}
}

func TestValidate_MissingProject(t *testing.T) {
	p := &PRD{UserStories: []UserStory{{ID: "A-1", Title: "t", Description: "d"}}}
	errs := Validate(p)
	if findErr(errs, "project") == nil {
		t.Fatalf("expected project error; got %+v", errs)
	}
}

func TestValidate_MissingRequiredFieldsPerStory(t *testing.T) {
	p := &PRD{
		Project: "x",
		UserStories: []UserStory{
			{}, // all fields missing
		},
	}
	errs := Validate(p)
	for _, want := range []string{
		"userStories[0].id",
		"userStories[0].title",
		"userStories[0].description",
	} {
		if findErr(errs, want) == nil {
			t.Errorf("expected error at %q; got %+v", want, errs)
		}
	}
}

func TestValidate_NegativePriorityRejected(t *testing.T) {
	p := &PRD{
		Project: "x",
		UserStories: []UserStory{{ID: "A-1", Title: "t", Description: "d", Priority: -1}},
	}
	errs := Validate(p)
	if findErr(errs, "userStories[0].priority") == nil {
		t.Fatalf("expected priority error; got %+v", errs)
	}
}

func TestValidate_DuplicateID(t *testing.T) {
	p := &PRD{
		Project: "x",
		UserStories: []UserStory{
			{ID: "A-1", Title: "t", Description: "d"},
			{ID: "A-1", Title: "t", Description: "d"},
		},
	}
	errs := Validate(p)
	e := findErr(errs, "userStories[1].id")
	if e == nil {
		t.Fatalf("expected duplicate id error; got %+v", errs)
	}
	if want := `duplicate id "A-1"`; !contains(e.Message, want) {
		t.Errorf("message=%q does not contain %q", e.Message, want)
	}
}

func TestValidate_DependsOnUnknown(t *testing.T) {
	p := &PRD{
		Project: "x",
		UserStories: []UserStory{
			{ID: "A-1", Title: "t", Description: "d", DependsOn: []string{"X-99"}},
		},
	}
	errs := Validate(p)
	e := findErr(errs, "userStories[0].dependsOn[0]")
	if e == nil {
		t.Fatalf("expected unknown dep error; got %+v", errs)
	}
}

func TestValidate_DependsOnSelf(t *testing.T) {
	p := &PRD{
		Project: "x",
		UserStories: []UserStory{
			{ID: "A-1", Title: "t", Description: "d", DependsOn: []string{"A-1"}},
		},
	}
	errs := Validate(p)
	if findErr(errs, "userStories[0].dependsOn[0]") == nil {
		t.Fatalf("expected self-dep error; got %+v", errs)
	}
}

func TestValidate_EmptyAcceptanceCriterion(t *testing.T) {
	p := &PRD{
		Project: "x",
		UserStories: []UserStory{
			{ID: "A-1", Title: "t", Description: "d", AcceptanceCriteria: []string{"ok", "  "}},
		},
	}
	errs := Validate(p)
	if findErr(errs, "userStories[0].acceptanceCriteria[1]") == nil {
		t.Fatalf("expected empty ac error; got %+v", errs)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
