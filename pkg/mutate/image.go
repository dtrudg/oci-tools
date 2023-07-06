// Copyright 2023 Sylabs Inc. All rights reserved.
//
// SPDX-License-Identifier: Apache-2.0

package mutate

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

type image struct {
	base      v1.Image
	overrides []v1.Layer
	history   *v1.History

	computed   bool
	diffIDs    []v1.Hash
	byDiffID   map[v1.Hash]v1.Layer
	byDigest   map[v1.Hash]v1.Layer
	manifest   *v1.Manifest
	configFile *v1.ConfigFile

	sync.Mutex
}

// populate populates various fields in img.
func (img *image) populate() error {
	img.Lock()
	defer img.Unlock()

	if img.computed {
		return nil
	}

	configFile, err := img.base.ConfigFile()
	if err != nil {
		return err
	}
	configFile = configFile.DeepCopy()

	manifest, err := img.base.Manifest()
	if err != nil {
		return err
	}
	manifest = manifest.DeepCopy()

	ls, err := img.base.Layers()
	if err != nil {
		return err
	}

	layers := make([]v1.Descriptor, 0, len(img.overrides))
	diffIDs := make([]v1.Hash, 0, len(img.overrides))
	byDiffID := make(map[v1.Hash]v1.Layer, len(img.overrides))
	byDigest := make(map[v1.Hash]v1.Layer, len(img.overrides))

	for i, l := range img.overrides {
		if l == nil {
			l = ls[i]
		}

		d, err := partial.Descriptor(l)
		if err != nil {
			return err
		}

		diffID, err := l.DiffID()
		if err != nil {
			return err
		}

		layers = append(layers, *d)
		diffIDs = append(diffIDs, diffID)
		byDiffID[diffID] = l
		byDigest[d.Digest] = l
	}

	manifest.Layers = layers
	configFile.RootFS.DiffIDs = diffIDs

	// Replace history, if applicable.
	if img.history != nil {
		configFile.History = []v1.History{*img.history}
	}

	config, err := json.Marshal(configFile)
	if err != nil {
		return err
	}

	digest, size, err := v1.SHA256(bytes.NewBuffer(config))
	if err != nil {
		return err
	}
	manifest.Config.Digest = digest
	manifest.Config.Size = size

	if manifest.Config.Data != nil {
		manifest.Config.Data = config
	}

	img.computed = true
	img.diffIDs = diffIDs
	img.byDiffID = byDiffID
	img.byDigest = byDigest
	img.manifest = manifest
	img.configFile = configFile

	return nil
}

// MediaType of this image's manifest.
func (img *image) MediaType() (types.MediaType, error) {
	return img.base.MediaType()
}

// Size returns the size of the manifest.
func (img *image) Size() (int64, error) {
	if err := img.populate(); err != nil {
		return 0, err
	}

	return partial.Size(img)
}

// Digest returns the sha256 of this image's manifest.
func (img *image) Digest() (v1.Hash, error) {
	if err := img.populate(); err != nil {
		return v1.Hash{}, err
	}

	return partial.Digest(img)
}

// Manifest returns this image's Manifest object.
func (img *image) Manifest() (*v1.Manifest, error) {
	if err := img.populate(); err != nil {
		return nil, err
	}

	return img.manifest, nil
}

// RawManifest returns the serialized bytes of Manifest().
func (img *image) RawManifest() ([]byte, error) {
	if err := img.populate(); err != nil {
		return nil, err
	}

	return json.Marshal(img.manifest)
}

// ConfigName returns the hash of the image's config file, also known as
// the Image ID.
func (img *image) ConfigName() (v1.Hash, error) {
	if err := img.populate(); err != nil {
		return v1.Hash{}, err
	}

	return partial.ConfigName(img)
}

// ConfigFile returns this image's config file.
func (img *image) ConfigFile() (*v1.ConfigFile, error) {
	if err := img.populate(); err != nil {
		return nil, err
	}

	return img.configFile, nil
}

// RawConfigFile returns the serialized bytes of ConfigFile().
func (img *image) RawConfigFile() ([]byte, error) {
	if err := img.populate(); err != nil {
		return nil, err
	}

	return json.Marshal(img.configFile)
}

// Layers returns the ordered collection of filesystem layers that comprise this image.
func (img *image) Layers() ([]v1.Layer, error) {
	if err := img.populate(); err != nil {
		return nil, err
	}

	ls := make([]v1.Layer, 0, len(img.diffIDs))

	for _, h := range img.diffIDs {
		l, err := img.LayerByDiffID(h)
		if err != nil {
			return nil, err
		}

		ls = append(ls, l)
	}

	return ls, nil
}

var errLayerNotFound = errors.New("layer not found")

// LayerByDigest returns a Layer for interacting with a particular layer of the image, looking it
// up by "digest" (the compressed hash).
func (img *image) LayerByDigest(h v1.Hash) (v1.Layer, error) {
	if err := img.populate(); err != nil {
		return nil, err
	}

	if l, ok := img.byDigest[h]; ok {
		return l, nil
	}

	return nil, errLayerNotFound
}

// LayerByDiffID is an analog to LayerByDigest, looking up by "diff id" (the uncompressed hash).
func (img *image) LayerByDiffID(h v1.Hash) (v1.Layer, error) {
	if err := img.populate(); err != nil {
		return nil, err
	}

	if l, ok := img.byDiffID[h]; ok {
		return l, nil
	}

	return nil, errLayerNotFound
}
