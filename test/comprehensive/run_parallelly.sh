#!/bin/bash

for i in {1..50}
do
  echo "Starting run $i"
  MONGO_URL=mongodb://localhost:27017/mongobouncer \
  go run test/comprehensive/main.go test/comprehensive/report.go --database "${i}_test_db" &
done

# Wait for all background jobs to finish
wait

echo "All 100 runs completed!"
