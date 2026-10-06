package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Resolve once per send, not once per process. Development changes are immediately
// visible; packaged binaries may supply KLM_PROMPTS_DIR or sibling prompts/.
func graphPrompt(name string, data any) (string, error) {
	body, err := loadInternalPrompt(name)
	if err != nil {
		return "", err
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", err
	}
	return body + "\n\n```json\n" + string(encoded) + "\n```\n", nil
}

func sessionCollaborationPrompt() (string, error) { return loadInternalPrompt("session-collaboration.md") }

func loadInternalPrompt(name string) (string, error) {
	if filepath.Base(name) != name || !strings.HasSuffix(name, ".md") {
		return "", errors.New("Invalid internal prompt name.")
	}
	dirs := []string{}
	if override := os.Getenv("KLM_PROMPTS_DIR"); override != "" {
		dirs = append(dirs, override)
	} else {
		if exe, err := os.Executable(); err == nil {
			dirs = append(dirs, filepath.Join(filepath.Dir(exe), "prompts"))
		}
		if _, source, _, ok := runtime.Caller(0); ok {
			dirs = append(dirs, filepath.Join(filepath.Dir(source), "prompts"))
		}
	}
	for _, dir := range dirs {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		if strings.TrimSpace(string(body)) == "" {
			return "", errors.New("Internal prompt is empty: " + name)
		}
		return string(body), nil
	}
	return "", errors.New("Cannot load internal prompt " + name + "; configure KLM_PROMPTS_DIR or package engine/prompts.")
}

func graphChoiceContracts(c *CompiledGraph, nodeID string) []map[string]any {
	choices := c.availableChoices(nodeID)
	result := []map[string]any{}
	for _, origin := range []string{"engine", "graph"} {
		ids := map[string]GraphChoice{}
		for identity, choice := range choices {
			if identity.Origin == origin {
				ids[identity.ID] = choice
			}
		}
		for _, id := range sortedGraphKeys(ids) {
			choice := ids[id]
			result = append(result, map[string]any{"choice": ChoiceIdentity{Origin: origin, ID: id}, "name": choice.Name, "description": choice.Description, "input": choice.Input, "terminal": choice.Terminal})
		}
	}
	return result
}
