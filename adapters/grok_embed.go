package adapterbundle

import "embed"

//go:embed all:grok
var grok embed.FS

// Grok is rooted at the plugin manifest the host loads.
var Grok = mustSub(grok, "grok")
