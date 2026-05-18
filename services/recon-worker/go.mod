module github.com/prasham/ledger/services/recon-worker

go 1.22

require (
	github.com/prasham/ledger/internal v0.0.0
	github.com/jackc/pgx/v5 v5.7.1
	github.com/prometheus/client_golang v1.20.5
	github.com/rs/zerolog v1.33.0
	github.com/shopspring/decimal v1.4.0
	github.com/stretchr/testify v1.9.0
	go.opentelemetry.io/otel v1.31.0
)

replace github.com/prasham/ledger/internal => ../../internal
