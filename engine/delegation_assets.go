package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
)

// Engine-owned assets, materialized only in the current adapter's private profile.
// Ordinary parents load the replacement; managed children load leaf restrictions.
//
//go:embed delegation-protocol.mjs
var delegationProtocolAsset string

//go:embed delegation-pi.mjs
var delegationPiAsset string

//go:embed delegation-opencode.mjs
var delegationOpenCodeAsset string

type DelegationAssetConfig struct {
	Enabled     bool   `json:"enabled"`
	ParentRole  string `json:"parentRole"`
	BridgeURL   string `json:"bridgeUrl"`
	BridgeToken string `json:"bridgeToken"`
}

func delegationAssetFiles(config DelegationAssetConfig) (map[string][]byte, error) {
	if config.Enabled {
		endpoint, err := url.Parse(config.BridgeURL)
		if err != nil || endpoint.Scheme != "http" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Port() == "" || config.BridgeToken == "" {
			return nil, errors.New("Delegation wrapper requires its owned authenticated loopback bridge.")
		}
		ip := net.ParseIP(endpoint.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("Delegation wrapper bridge must be a literal loopback address.")
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	manifest, err := delegationCodexManifestV1()
	if err != nil {
		return nil, err
	}
	metadata, err := json.Marshal(map[string]any{"version": delegationSchemaVersion, "name": delegationToolName, "description": delegationToolDescription, "schema": delegationToolSchemaV1(), "specDigest": manifest.SpecDigest, "maxChildren": delegationMaxChildren, "maxConcurrency": delegationConcurrency, "activeBatchLimit": 1})
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		"delegation-protocol.mjs": []byte(strings.Replace(delegationProtocolAsset, "/*KLM_DELEGATION_METADATA*/{}", string(metadata), 1)),
		"delegation-pi.mjs":       []byte(strings.Replace(delegationPiAsset, "/*KLM_DELEGATION_CONFIG*/{}", string(encoded), 1)),
		"delegation-opencode.mjs": []byte(strings.Replace(delegationOpenCodeAsset, "/*KLM_DELEGATION_CONFIG*/{}", string(encoded), 1)),
	}, nil
}
