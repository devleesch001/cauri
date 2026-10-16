package store

// The migrations written in Go, alone, for the tests of package
// store_test, which cover both engines: 00009 and 00011.
var (
	ProjectUp, ProjectDown       = projectUp, projectDown
	GeoSourcesUp, GeoSourcesDown = geoSourcesUp, geoSourcesDown
)
