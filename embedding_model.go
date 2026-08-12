package main

import "fmt"

// embeddingModel is the contract shared by storage, the HTTP API, and the
// browser worker. A new model gets its own key and store file so vectors from
// incompatible models can never be mixed.
type embeddingModel struct {
	Key        string `json:"key"`
	Backend    string `json:"backend"`
	ModelID    string `json:"modelId"`
	Revision   string `json:"revision"`
	DType      string `json:"dtype"`
	Dimensions int    `json:"dimensions"`
	storeFile  string
}

var defaultEmbeddingModel = embeddingModel{
	Key:        "clip-vit-base-patch32-q8-v1",
	Backend:    "clip",
	ModelID:    "Xenova/clip-vit-base-patch32",
	Revision:   "d15189d7028b43f1d3e65039190477f6af591c2a",
	DType:      "q8",
	Dimensions: 512,
	storeFile:  "embeddings-v1.bin",
}

func (model embeddingModel) validate() error {
	if model.Key == "" || model.Backend == "" || model.ModelID == "" || model.Revision == "" || model.DType == "" {
		return fmt.Errorf("embedding model metadata is incomplete")
	}
	if model.Dimensions < 1 || model.Dimensions > 1<<20 {
		return fmt.Errorf("invalid embedding dimensions")
	}
	if model.storeFile == "" {
		return fmt.Errorf("embedding model store is not configured")
	}
	return nil
}
