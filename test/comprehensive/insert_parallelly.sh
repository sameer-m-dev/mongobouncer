#!/bin/bash

for i in {1..10}
do
  echo "Inserting run $i"

  mongosh "mongodb://localhost:27017/mongobouncer_test" \
    --eval "db.testRuns.insertOne({ run: $i })" &
done

wait

echo "All 5 inserts completed!"
