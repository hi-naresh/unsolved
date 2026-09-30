// Package migrations embeds the goose SQL migrations so the migrate command
// and tests apply exactly the committed files.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
