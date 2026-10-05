// Package api embeds the OpenAPI contract so the server publishes the same
// document that lives in version control.
package api

import _ "embed"

//go:embed openapi.yaml
var OpenAPI []byte
