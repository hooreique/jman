package jman

import "testing"

func TestClassifyProblemsSeparatesCallerAndTestErrors(t *testing.T) {
	model := &BuildModel{Contexts: []BuildContext{
		{Project: ":caller", SourceSet: "main", Roots: []string{"/workspace/caller/src/main/java"}, CompileClasspath: []string{"/workspace/dependency/build/classes"}},
		{Project: ":dependency", SourceSet: "main", Roots: []string{"/workspace/dependency/src/main/java"}, Outputs: []string{"/workspace/dependency/build/classes"}},
		{Project: ":other", SourceSet: "test", Roots: []string{"/workspace/other/src/test/java"}},
	}}
	caller := &model.Contexts[0]
	problems := []any{
		map[string]any{"location": "/workspace/caller/src/main/java/Bad.java"},
		map[string]any{"location": "/workspace/dependency/src/main/java/Bad.java"},
		map[string]any{"location": "/workspace/other/src/test/java/BadTest.java"},
		map[string]any{"path": "/unmapped/Bad.java"},
	}
	got := classifyProblems(model, caller, problems, false)
	if len(got.Critical) != 2 || len(got.Advisory) != 1 || len(got.Unknown) != 1 {
		t.Fatalf("unexpected diagnostics: %#v", got)
	}
	strict := classifyProblems(model, caller, problems, true)
	if len(strict.Critical) != 4 || len(strict.Advisory) != 0 || len(strict.Unknown) != 0 {
		t.Fatalf("unexpected strict diagnostics: %#v", strict)
	}
}

func TestDescribeProblemsMapsSourceSet(t *testing.T) {
	model := &BuildModel{Contexts: []BuildContext{{Project: ":app", SourceSet: "main", Roots: []string{"/workspace/app/src/main/java"}}}}
	got := describeProblems(model, []any{map[string]any{"location": "/workspace/app/src/main/java/Bad.java"}})
	problem := got[0].(map[string]any)
	if problem["project"] != ":app" || problem["sourceSet"] != "main" {
		t.Fatalf("unexpected problem mapping: %v", problem)
	}
}

func TestClassifyProblemsUsesCallerSourceSetDependencies(t *testing.T) {
	model := &BuildModel{Contexts: []BuildContext{
		{Project: ":app", SourceSet: "main", Roots: []string{"/w/app/main"}},
		{Project: ":app", SourceSet: "test", Roots: []string{"/w/app/test"}, Artifacts: []Artifact{{ProjectComponent: true, ProjectPath: ":testlib"}}},
		{Project: ":app", SourceSet: "integrationTest", Roots: []string{"/w/app/integration"}, Artifacts: []Artifact{{ProjectComponent: true, ProjectPath: ":testlib"}}},
		{Project: ":testlib", SourceSet: "main", Roots: []string{"/w/lib/main"}, Artifacts: []Artifact{{ProjectComponent: true, ProjectPath: ":transitive"}}},
		{Project: ":testlib", SourceSet: "test", Roots: []string{"/w/lib/test"}},
		{Project: ":transitive", SourceSet: "main", Roots: []string{"/w/transitive/main"}, Artifacts: []Artifact{{ProjectComponent: true, ProjectPath: ":testlib"}}},
	}}
	model.Contexts[0].Outputs = []string{"/out/app/main"}
	model.Contexts[3].Outputs = []string{"/out/lib/main"}
	model.Contexts[5].Outputs = []string{"/out/transitive/main"}
	for _, index := range []int{1, 2} {
		model.Contexts[index].CompileClasspath = []string{"/out/app/main", "/out/lib/main", "/out/transitive/main"}
	}
	problems := []any{}
	for _, c := range model.Contexts {
		problems = append(problems, map[string]any{"location": c.Roots[0] + "/Bad.java"})
	}
	for _, tc := range []struct{ index, critical, advisory int }{{0, 1, 5}, {1, 4, 2}, {2, 4, 2}} {
		got := classifyProblems(model, &model.Contexts[tc.index], problems, false)
		if len(got.Critical) != tc.critical || len(got.Advisory) != tc.advisory || len(got.Unknown) != 0 {
			t.Fatalf("caller %s: %#v", model.Contexts[tc.index].SourceSet, got)
		}
	}
}

func TestClassifyProblemsUsesSelectedVariantNotProducerDependencies(t *testing.T) {
	model := &BuildModel{Contexts: []BuildContext{
		{Project: ":app", SourceSet: "integration", Roots: []string{"/w/app/integration"}, CompileClasspath: []string{"/out/app/main"}, Artifacts: []Artifact{{Path: "/out/lib/test.jar", ProjectComponent: true, ProjectPath: ":lib"}}},
		{Project: ":app", SourceSet: "main", Roots: []string{"/w/app/main"}, Outputs: []string{"/out/app/main"}},
		{Project: ":lib", SourceSet: "test", Roots: []string{"/w/lib/test"}, Outputs: []string{"/out/lib/test.jar"}, CompileClasspath: []string{"/out/excluded/main"}},
		{Project: ":lib", SourceSet: "main", Roots: []string{"/w/lib/main"}, Outputs: []string{"/out/lib/main"}},
		{Project: ":excluded", SourceSet: "main", Roots: []string{"/w/excluded/main"}, Outputs: []string{"/out/excluded/main"}},
	}}
	var problems []any
	for _, c := range model.Contexts {
		problems = append(problems, map[string]any{"location": c.Roots[0] + "/Bad.java"})
	}
	got := classifyProblems(model, &model.Contexts[0], problems, false)
	if len(got.Critical) != 3 || len(got.Advisory) != 2 {
		t.Fatalf("selected classpath ignored: %#v", got)
	}
}
