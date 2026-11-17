// Package tools implements every devknife utility. Each tool is a pure
// function registered in a shared registry used by the CLI, web UI and MCP.
package tools

import "devknife/internal/core"

func inspectTools() []*core.Tool {
	return []*core.Tool{unixTimeTool(), jwtTool(), regexTool(), htmlPreviewTool(), diffTool(), stringInspectorTool(), markdownTool(), cronTool(), colorTool(), jsonQueryTool()}
}

// Registry returns a registry with all tools.
func Registry() *core.Registry {
	r := core.NewRegistry()
	r.Add(formatTools()...)
	r.Add(convertTools()...)
	r.Add(inspectTools()...)
	r.Add(generateTools()...)
	r.Add(encodeTools()...)
	r.Add(commandsTool())
	return r
}
