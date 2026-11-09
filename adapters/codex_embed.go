package adapterbundle

import "embed"

//go:embed all:codex
var codex embed.FS

var Codex = mustSub(codex, "codex")
