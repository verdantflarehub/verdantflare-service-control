ARG GOLANG_BASE=docker.io/library/golang:1.25-alpine

FROM ${GOLANG_BASE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/control ./cmd/control

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/control /control
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/control"]
