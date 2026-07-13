// Package migrations holds the goose SQL migration files, embedded into the
// binary so they can be applied in-process at runtime with no external files.
package migrations

import "embed"

// FS contains every migration in this directory, embedded via embed.FS.
//
//go:embed *.sql
var FS embed.FS
