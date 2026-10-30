//go:build race

package scenarios

const raceOn = true

// rightsPairs is how many pairs of manifests the property test of rights tries; the race detector is ten times slower.
const rightsPairs = 40
