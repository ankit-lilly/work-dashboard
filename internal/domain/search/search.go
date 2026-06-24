package search

import "time"

type RecordMatch struct {
	Index  int
	Record string
}

type StringMatch struct {
	Index  int
	Record string
}

type ObjectInfo struct {
	Key          string
	SizeBytes    int64
	LastModified time.Time
}
