module github.com/mrjvadi/torncity

go 1.25.7

require (
	github.com/jackc/pgx/v5 v5.11.0
	github.com/nats-io/nats.go v1.46.1
	github.com/redis/go-redis/v9 v9.11.0
	golang.org/x/crypto v0.37.0
	golang.org/x/term v0.31.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/nats-io/nkeys v0.4.11 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/sys v0.32.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

// The web panel's npm dependencies are not Go packages.
ignore ./web/panel/node_modules
