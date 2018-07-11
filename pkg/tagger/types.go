// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2018 Datadog, Inc.

package tagger

// TaggerListResponse holds the tagger list response
type TaggerListResponse struct {
	Entities map[string]TaggerListEntity `json:"entities"`
}

// TaggerListEntity holds the tagging info about an entity
type TaggerListEntity struct {
	Sources []string `json:"sources"`
	Tags    []string `json:"tags"`
}
