#!/bin/bash

for i in {1..100}
do
  echo "Run $i"
  MONGO_URL=mongodb://localhost:27017/mongobouncer go run test/comprehensive/main.go test/comprehensive/report.go --database "$i"_test_db
  sleep 1.5
done
