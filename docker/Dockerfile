ARG GOLANG_BASE=docker.io/library/golang:1.25-alpine

FROM ${GOLANG_BASE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/control ./cmd/control
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/survey-storage ./cmd/survey-storage
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/skill-ops-role ./cmd/skill-ops-role

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/control /control
COPY --from=build /out/survey-storage /survey-storage
COPY --from=build /out/skill-ops-role /skill-ops-role
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/control"]
