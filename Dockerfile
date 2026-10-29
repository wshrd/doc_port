# build
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/docport ./cmd/server

# run
FROM scratch
COPY --from=build /out/docport /docport
EXPOSE 80
USER 65532:65532
ENTRYPOINT ["/docport"]
