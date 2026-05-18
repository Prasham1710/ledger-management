module github.com/prasham/ledger/services/gateway

go 1.22

require (
	github.com/prasham/ledger/internal v0.0.0
	github.com/cenkalti/backoff/v4 v4.3.0
	github.com/gin-gonic/gin v1.10.0
	github.com/google/uuid v1.6.0
	github.com/redis/go-redis/v9 v9.7.0
	github.com/rs/zerolog v1.33.0
	github.com/shopspring/decimal v1.4.0
	github.com/stretchr/testify v1.9.0
	go.opentelemetry.io/otel v1.31.0
	google.golang.org/grpc v1.68.0
	google.golang.org/protobuf v1.35.1
)

replace github.com/prasham/ledger/internal => ../../internal
