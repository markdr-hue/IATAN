/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package main

import "embed"

//go:embed all:web/admin
var adminFS embed.FS

//go:embed web/foundation/iatan.js
var foundationJS []byte
