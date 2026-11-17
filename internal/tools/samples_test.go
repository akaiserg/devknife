package tools

import (
	"os"
	"strings"
	"testing"

	"devknife/internal/core"
)

// Every tool's built-in sample (and every template) must run cleanly.
func TestSamplesRun(t *testing.T) {
	reg := Registry()
	for _, tool := range reg.All() {
		t.Run(tool.ID, func(t *testing.T) {
			res, err := tool.Execute(core.Request{Input: tool.Sample, Opts: map[string]string{}})
			if tool.ID == "regex" {
				res, err = tool.Execute(core.Request{Input: tool.Sample, Opts: map[string]string{"pattern": `\b[A-Z]\w+`}})
			}
			if tool.ID == "diff" {
				res, err = tool.Execute(core.Request{Input: tool.Templates[0].Input, Opts: tool.Templates[0].Opts})
			}
			if err != nil {
				t.Fatalf("sample failed: %v", err)
			}
			if os.Getenv("SHOW") != "" && (os.Getenv("SHOW") == "all" || strings.Contains(","+os.Getenv("SHOW")+",", ","+tool.ID+",")) {
				t.Logf("\n=== %s ===\n%s\n--- fields: %v\n--- warnings: %v", tool.ID, res.Output, res.Fields, res.Warnings)
			}
			for _, tpl := range tool.Templates {
				in := tpl.Input
				if in == "" {
					in = tool.Sample
				}
				if _, err := tool.Execute(core.Request{Input: in, Opts: tpl.Opts}); err != nil {
					t.Errorf("template %q failed: %v", tpl.Name, err)
				}
			}
		})
	}
}
