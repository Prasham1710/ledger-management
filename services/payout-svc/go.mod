module github.com/prasham/ledger/services/payout-svc

go 1.22

require (
	github.com/prasham/ledger/internal v0.0.0
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.7.1
	github.com/minio/minio-go/v7 v7.0.80
	github.com/rs/zerolog v1.33.0
	github.com/shopspring/decimal v1.4.0
	github.com/stretchr/testify v1.9.0
	go.opentelemetry.io/otel v1.31.0
	google.golang.org/grpc v1.68.0
	google.golang.org/protobuf v1.35.1
)

replace github.com/prasham/ledger/internal => ../../internal
