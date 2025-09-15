#!/bin/bash

for i in {1..50}
do
  echo "Inserting run $i"

  mongosh "mongodb://localhost:27017/mongobouncer_test?retryWrites=false" \
    --eval "db.testRuns.insertOne({ run: $i })" &
done

wait

echo "All 5 inserts completed!"
