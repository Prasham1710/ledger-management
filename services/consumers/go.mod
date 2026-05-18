module github.com/prasham/ledger/services/consumers

go 1.22

require (
	github.com/prasham/ledger/internal v0.0.0
	github.com/cenkalti/backoff/v4 v4.3.0
	github.com/jackc/pgx/v5 v5.7.1
	github.com/rs/zerolog v1.33.0
	github.com/stretchr/testify v1.9.0
	github.com/twmb/franz-go v1.18.0
	github.com/twmb/franz-go/pkg/sr v1.3.0
	go.opentelemetry.io/otel v1.31.0
)

replace github.com/prasham/ledger/internal => ../../internal
