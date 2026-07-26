package oci

import (
	"errors"

	"github.com/integrio-intropy/intropy-cli/internal/registry"
)

var (
	ErrNotFound     = registry.ErrNotFound
	ErrNotSkill     = errors.New("skill: artifact is not a skill.v1")
	ErrUnauthorized = registry.ErrUnauthorized
)
