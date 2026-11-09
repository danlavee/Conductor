package adapterbundle

import "embed"

//go:embed all:agy
var agy embed.FS

// AGY is rooted at the plugin manifest the host loads.
var AGY = mustSub(agy, "agy")
