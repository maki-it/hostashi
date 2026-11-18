module github.com/maki-it/hostashi/spike/s6-adopt

go 1.24

require github.com/maki-it/hostashi/spike/s1-agent-rpc v0.0.0-00010101000000-000000000000

require golang.org/x/sys v0.33.0

// Local path on purpose: S1 is a sibling spike, not a published module.
replace github.com/maki-it/hostashi/spike/s1-agent-rpc => ../s1-agent-rpc
