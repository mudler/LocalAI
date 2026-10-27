package triggers

// The zone database is part of the binary: a schedule names a time zone, and
// the official image has no zoneinfo files. This is a blank import in a library
// package on purpose, so that every build and test binary has the data.
import _ "time/tzdata"
