package store

// ProjectUp and ProjectDown are migration 00009 alone, for the tests of
// package store_test, which cover both engines.
var ProjectUp, ProjectDown = projectUp, projectDown
