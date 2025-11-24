FROM golang:1.19-alpine

WORKDIR /app

RUN apk add --no-cache curl

COPY go.mod *.go ./
RUN go mod download

EXPOSE 8080

CMD ["go", "run", "main.go"]