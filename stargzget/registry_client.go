package stargzget

import "context"

type RegistryClient interface {
	GetManifest(ctx context.Context, imageRef string) (*Manifest, error)
}

type Manifest struct {
	Layers []Layer
}

type Layer struct {
	Digest string
}
