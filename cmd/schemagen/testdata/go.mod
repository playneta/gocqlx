module schemagentest

go 1.21

require github.com/google/go-cmp v0.7.0

require (
	github.com/apache/cassandra-gocql-driver/v2 v2.1.2
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.17.9 // indirect
	github.com/playneta/gocqlx v0.0.0
	github.com/scylladb/go-reflectx v1.0.1 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
)

replace github.com/playneta/gocqlx => ../../..
