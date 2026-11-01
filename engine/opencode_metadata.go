package main

func compactOpenCodeTokens(value any) map[string]any {
	tokens := object(value)
	cache := object(tokens["cache"])
	return map[string]any{"input": tokens["input"], "output": tokens["output"], "reasoning": tokens["reasoning"], "cache": map[string]any{"read": cache["read"], "write": cache["write"]}}
}

func compactOpenCodeInfo(info map[string]any) map[string]any {
	result := map[string]any{}
	for _, key := range []string{"id", "sessionID", "parentID", "role", "providerID", "modelID", "variant", "finish"} {
		result[key] = info[key]
	}
	times := object(info["time"])
	result["time"] = map[string]any{"created": times["created"], "completed": times["completed"]}
	result["tokens"] = compactOpenCodeTokens(info["tokens"])
	result["summary"] = truth(info, "summary")
	if info["error"] != nil {
		result["error"] = map[string]any{"name": str(object(info["error"]), "name"), "message": humanError(info["error"])}
	}
	return result
}
