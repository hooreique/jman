package jman

import (
	"path/filepath"
	"strconv"
	"strings"
)

type diagnosticSummary struct {
	Critical []any `json:"critical"`
	Advisory []any `json:"advisory"`
	Unknown  []any `json:"unknown"`
}

// classifyProblems promotes caller-source-set and compile-closure errors. Other
// mapped errors remain visible as advisory rather than hiding uncertainty.
func classifyProblems(model *BuildModel, caller *BuildContext, problems []any, strict bool) diagnosticSummary {
	summary := diagnosticSummary{Critical: []any{}, Advisory: []any{}, Unknown: []any{}}
	closure := model.compileClosure(caller)
	for _, problem := range problems {
		if strict {
			summary.Critical = append(summary.Critical, problem)
			continue
		}
		path := problemLocation(problem)
		context := model.context(path)
		switch {
		case context == nil:
			summary.Unknown = append(summary.Unknown, problem)
		case closure[context]:
			summary.Critical = append(summary.Critical, problem)
		default:
			summary.Advisory = append(summary.Advisory, problem)
		}
	}
	return summary
}

func problemLocation(problem any) string {
	values, ok := problem.(map[string]any)
	if !ok {
		return ""
	}
	path, _ := values["location"].(string)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func describeProblems(model *BuildModel, problems []any) []any {
	described := make([]any, 0, len(problems))
	for _, problem := range problems {
		values, ok := problem.(map[string]any)
		if !ok {
			described = append(described, problem)
			continue
		}
		copy := make(map[string]any, len(values)+3)
		for key, value := range values {
			copy[key] = value
		}
		if context := model.context(problemLocation(problem)); context != nil {
			copy["project"] = context.Project
			copy["sourceSet"] = context.SourceSet
			copy["sourceRoot"] = longestProblemRoot(context.Roots, problemLocation(problem))
		} else {
			copy["sourceSet"] = "unknown"
		}
		described = append(described, copy)
	}
	return described
}

func longestProblemRoot(roots []string, path string) string {
	best := ""
	for _, root := range roots {
		if strings.HasPrefix(path, root+string(filepath.Separator)) && len(root) > len(best) {
			best = root
		}
	}
	return best
}

func (s diagnosticSummary) complete() bool {
	return len(s.Advisory) == 0 && len(s.Unknown) == 0 && len(s.Critical) == 0
}
func (s diagnosticSummary) warning() string {
	parts := []string{}
	if len(s.Advisory) > 0 {
		parts = append(parts, "advisory errors: "+strconv.Itoa(len(s.Advisory)))
	}
	if len(s.Unknown) > 0 {
		parts = append(parts, "unmapped errors: "+strconv.Itoa(len(s.Unknown)))
	}
	return "Workspace diagnostics may limit global coverage (" + strings.Join(parts, ", ") + "); inspect doctor output."
}
