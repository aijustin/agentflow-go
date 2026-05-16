package main

import (
	"path/filepath"

	blobfile "github.com/aijustin/agentflow-go/internal/adapter/blob/file"
	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	runstatefile "github.com/aijustin/agentflow-go/internal/adapter/runstate/file"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func newRunStores(stateDir string) (runstate.Repository, runstate.BlobStore, error) {
	if stateDir == "" {
		return runstateinmem.NewRepository(), blobinmem.NewStore(), nil
	}
	repo, err := runstatefile.NewRepository(filepath.Join(stateDir, "runs"))
	if err != nil {
		return nil, nil, err
	}
	blobs, err := blobfile.NewStore(filepath.Join(stateDir, "blobs"))
	if err != nil {
		return nil, nil, err
	}
	return repo, blobs, nil
}

func newRunRepository(stateDir string) (runstate.Repository, error) {
	if stateDir == "" {
		return runstateinmem.NewRepository(), nil
	}
	return runstatefile.NewRepository(filepath.Join(stateDir, "runs"))
}
