FROM golang:1.24 AS build
WORKDIR /go/src
COPY . .
RUN CGO_ENABLED=0 make


FROM scratch
COPY --from=build /go/src/bin/mongobouncer /mongobouncer
ENTRYPOINT ["/mongobouncer"]
