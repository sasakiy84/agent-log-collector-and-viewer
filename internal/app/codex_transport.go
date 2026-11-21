package app

// Code Mode puts its runtime session ID in session.id. These spans record the
// transport setup and tool dispatch around events recorded elsewhere.
func codeModeTransportOnly(e Event) bool {
	if e.Source != "codex" || e.Origin != "otel" || e.Signal != "traces" {
		return false
	}
	module := "codex_code_mode::grpc_session"
	switch e.Name {
	case "code_mode.grpc.callback":
		module += "::callbacks"
	case "code_mode.grpc.subscribe_to_tool_calls":
	case "code_mode.grpc.open_binding", "code_mode.grpc.open_session":
		// Nested connection spans can bridge a parent operation to its children.
		if e.ParentSpan != "" {
			return false
		}
	default:
		return false
	}
	doc, err := decodeDocument(e.Raw)
	if err != nil || doc.Record["name"] != e.Name {
		return false
	}
	kind := str(doc.Record["kind"])
	if kind != "SPAN_KIND_INTERNAL" && kind != "1" {
		return false
	}
	status := obj(doc.Record["status"])
	switch str(status["code"]) {
	case "", "0", "1", "STATUS_CODE_UNSET", "STATUS_CODE_OK":
	default:
		return false
	}
	if str(status["message"]) != "" || len(arr(doc.Record["events"])) != 0 || len(arr(doc.Record["links"])) != 0 {
		return false
	}
	for _, field := range []string{"droppedAttributesCount", "droppedEventsCount", "droppedLinksCount"} {
		if value := str(doc.Record[field]); value != "" && value != "0" {
			return false
		}
	}
	recordAttrs := decodedAttributes(doc.Record["attributes"])
	sessionID, _ := recordAttrs["session.id"].(string)
	if (e.Name == "code_mode.grpc.callback" || e.Name == "code_mode.grpc.subscribe_to_tool_calls") && sessionID == "" {
		return false
	}
	if recordAttrs["code.module.name"] != module ||
		decodedAttributes(doc.Resource["attributes"])["service.name"] != "codex-app-server" {
		return false
	}
	// Preserve unknown attributes in case a newer exporter includes tool
	// results, usage, or another signal useful for understanding behavior.
	for key := range doc.attributes() {
		switch key {
		case "service.name", "service.version", "service.namespace", "project.name", "project.tags", "env",
			"telemetry.sdk.name", "telemetry.sdk.version", "telemetry.sdk.language",
			"code.file.path", "code.module.name", "code.line.number", "target",
			"thread.id", "thread.name", "session.id", "busy_ns", "idle_ns",
			"execution.id", "cell.id", "invocation.id":
		default:
			return false
		}
	}
	return true
}

func withoutCodeModeTransport(events []Event) []Event {
	var discard []bool
	for i, event := range events {
		if codeModeTransportOnly(event) {
			if discard == nil {
				discard = make([]bool, len(events))
			}
			discard[i] = true
		}
	}
	if discard == nil {
		return events
	}
	// Keep transport spans in mixed traces when a batch includes other
	// Codex records that may rely on the span to resolve their ancestry.
	mixed := make(map[string]bool)
	for i, event := range events {
		if event.Source == "codex" && event.Trace != "" && !discard[i] {
			mixed[event.Trace] = true
		}
	}
	retained := make([]Event, 0, len(events))
	for i, event := range events {
		if !discard[i] || mixed[event.Trace] {
			retained = append(retained, event)
		}
	}
	return retained
}
