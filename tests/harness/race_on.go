//go:build race

package harness

const raceOn = true

// RightsPairs is how many pairs of manifests the property test of rights tries; the race detector is ten times slower.
const RightsPairs = 40
