// Package upgrade raises what an older deck left to what this one reads: the Go migrations of the base, run by
// store.Open beside its SQL ones, and the manifests a module brings from outside.
package upgrade

// Migrations are the Go migrations of the base, by version.
var Migrations = append(commandsRenamed, format15, format16, masterSleep, format17)
