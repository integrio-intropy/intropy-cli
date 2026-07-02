package oci

import "errors"

var (
	ErrNotFound     = errors.New("skill: artifact not found")
	ErrNotSkill     = errors.New("skill: artifact is not a skill.v1")
	ErrUnauthorized = errors.New("skill: unauthorized")
)
