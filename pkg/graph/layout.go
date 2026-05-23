package graph

// MergeLayout copies canvas layout from edited onto exported, preserving node positions after save/reload.
func MergeLayout(edited, exported ScenarioGraph) ScenarioGraph {
	if edited.Workflow != nil && exported.Workflow != nil && len(edited.Workflow.Layout) > 0 {
		if exported.Workflow.Layout == nil {
			exported.Workflow.Layout = make(map[string]GraphPosition, len(edited.Workflow.Layout))
		}
		for id, pos := range edited.Workflow.Layout {
			exported.Workflow.Layout[id] = pos
		}
	}
	if len(edited.Workflows) > 0 {
		if exported.Workflows == nil {
			exported.Workflows = make(map[string]GraphView, len(edited.Workflows))
		}
		for name, editedView := range edited.Workflows {
			exportedView, ok := exported.Workflows[name]
			if !ok {
				exportedView = GraphView{ID: name}
			}
			if len(editedView.Layout) > 0 {
				if exportedView.Layout == nil {
					exportedView.Layout = make(map[string]GraphPosition, len(editedView.Layout))
				}
				for id, pos := range editedView.Layout {
					exportedView.Layout[id] = pos
				}
			}
			exported.Workflows[name] = exportedView
		}
	}
	return exported
}
