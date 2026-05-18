saga pattern -> means all things which are in row should als succesfully pass too
svc -> service

Apicurio is a central place that stores the exact shape of every message — what fields it has, what types they are, whether they're required. This "shape" is called a schema.


Apicurio is the contract enforcer for your Kafka messages — it makes sure producers and consumers always speak the same language, and blocks any change that would break that agreement before it ever reaches production.