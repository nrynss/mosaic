//go:build tools

// Package tools pins development-only command dependencies for this module.
package tools

import (
	_ "go.uber.org/mock/mockgen"
)
