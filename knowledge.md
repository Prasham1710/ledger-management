saga pattern -> means all things which are in row should als succesfully pass too
svc -> service

Apicurio is a central place that stores the exact shape of every message — what fields it has, what types they are, whether they're required. This "shape" is called a schema.


Apicurio is the contract enforcer for your Kafka messages — it makes sure producers and consumers always speak the same language, and blocks any change that would break that agreement before it ever reaches production.
| What              | Purpose                                        |
|-------------------|------------------------------------------------|
| PostgreSQL        | Database — where all money data lives          |
| Redis             | Fast cache — duplicate payment checks, rate limits |
| Redpanda          | Message broker — services talk to each other   |
| Apicurio          | Message contract enforcer                      |
| MinIO             | File storage — bank transfer files             |
| Debezium          | Watches database, auto-publishes events to Kafka |
| Toxiproxy         | Simulate network failures for testing          |
| pgAdmin           | Browse your database in browser                |
| RedisInsight      | Browse your cache in browser                   |
| Redpanda Console  | Browse your Kafka topics in browser            |
| MinIO Console     | Browse your files in browser                   |

A **.proto** file is a contract written in a language called Protocol Buffers

**RPC / gRPC**The mechanism of making the call across the network