package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed prompts/*.md
var packagedInternalPrompts embed.FS

// Resolve once per send, not once per process. Editable overrides and development
// sources take precedence over the packaged defaults.
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

func sessionCollaborationPrompt() (string, error) {
	return loadInternalPrompt("session-collaboration.md")
}

func loadInternalPrompt(name string) (string, error) {
	if filepath.Base(name) != name || !strings.HasSuffix(name, ".md") {
		return "", errors.New("Invalid internal prompt name.")
	}
	dirs := []string{}
	override := os.Getenv("KLM_PROMPTS_DIR")
	if override != "" {
		dirs = append(dirs, override)
	} else {
		if exe, err := os.Executable(); err == nil {
			dirs = append(dirs, filepath.Join(filepath.Dir(exe), "prompts"))
		}
		// With -trimpath Caller returns a module-relative path, not a source
		// location. Never resolve that path against the worker's cwd (where
		// "klm" can be the executable rather than a directory).
		if _, source, _, ok := runtime.Caller(0); ok && filepath.IsAbs(source) {
			dirs = append(dirs, filepath.Join(filepath.Dir(source), "prompts"))
		}
	}
	for _, dir := range dirs {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) && override == "" {
				continue
			}
			return "", fmt.Errorf("Cannot load internal prompt %s: %w", name, err)
		}
		return internalPromptText(name, body)
	}
	body, err := packagedInternalPrompts.ReadFile("prompts/" + name)
	if err != nil {
		return "", fmt.Errorf("Cannot load packaged internal prompt %s: %w", name, err)
	}
	return internalPromptText(name, body)
}

func internalPromptText(name string, body []byte) (string, error) {
	if strings.TrimSpace(string(body)) == "" {
		return "", errors.New("Internal prompt is empty: " + name)
	}
	return string(body), nil
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
