//go:build race

package testutil

// Race reports whether the tests run under the race detector. Under it a
// test takes ten times as long or more; a test that repeats one check over
// many cases of code without goroutines can run fewer of them.
const Race = true
