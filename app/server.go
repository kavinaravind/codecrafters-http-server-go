package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	StatusOK                  = "HTTP/1.1 200 OK\r\n\r\n"
	StatusCreated             = "HTTP/1.1 201 Created\r\n\r\n"
	StatusNotFound            = "HTTP/1.1 404 Not Found\r\n\r\n"
	StatusBadRequest          = "HTTP/1.1 400 Bad Request\r\n\r\n"
	StatusInternalServerError = "HTTP/1.1 500 Internal Server Error\r\n\r\n"
)

func main() {
	l, err := net.Listen("tcp", "0.0.0.0:4221")
	if err != nil {
		fmt.Println("Failed to bind to port 4221")
		os.Exit(1)
	}
	defer l.Close()

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Printf("Error accepting connection: %s\n", err.Error())
			continue
		}
		go handleConnection(conn)
	}
}

// handleConnection handles the incoming connection
func handleConnection(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	lines, request, path, err := readRequest(reader)
	if err != nil {
		fmt.Printf("Error reading request: %s\n", err.Error())
		writer.WriteString(StatusBadRequest)
		writer.Flush()
		return
	}

	switch {
	case path == "":
		writer.WriteString(StatusOK)
	case path == "user-agent":
		handleUserAgentRequest(writer, lines)
	case strings.HasPrefix(path, "echo/"):
		handleEchoRequest(writer, lines, path)
	case strings.HasPrefix(path, "files/"):
		handleFileRequest(reader, writer, request[0], lines, path)
	default:
		writer.WriteString(StatusNotFound)
	}

	writer.Flush()
}

// readRequest reads the HTTP request from the client
func readRequest(reader *bufio.Reader) ([]string, []string, string, error) {
	var lines []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				return nil, nil, "", err
			}
			break
		}

		line = strings.TrimSuffix(line, "\r\n")
		lines = append(lines, line)

		// If the line is empty, we have reached the end of the HTTP request header
		if line == "" {
			break
		}
	}

	if len(lines) == 0 {
		return nil, nil, "", errors.New("empty request")
	}

	request := strings.Split(lines[0], " ")
	if len(request) == 0 {
		return nil, nil, "", errors.New("invalid request")
	}

	path := strings.Trim(request[1], "/")

	return lines, request, path, nil
}

// handleUserAgentRequest will handle requests for user-agent
func handleUserAgentRequest(writer *bufio.Writer, lines []string) {
	userAgent := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "User-Agent: ") {
			userAgent = strings.TrimPrefix(line, "User-Agent: ")
			break
		}
	}

	res := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s", len(userAgent), userAgent)
	writer.WriteString(res)
}

// handleEchoRequest will handle requests for echo
func handleEchoRequest(writer *bufio.Writer, lines []string, path string) {
	acceptEncoding := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "Accept-Encoding: ") {
			acceptEncoding = strings.TrimPrefix(line, "Accept-Encoding: ")
			break
		}
	}

	contentEncodingHeader := ""
	for _, encoding := range strings.Split(acceptEncoding, " ") {
		encoding = strings.TrimSuffix(encoding, ",")
		if encoding == "gzip" {
			contentEncodingHeader = "Content-Encoding: gzip\r\n"
		}
	}

	word := strings.TrimPrefix(path, "echo/")

	var body bytes.Buffer
	if contentEncodingHeader != "" {
		zw := gzip.NewWriter(&body)
		zw.Write([]byte(word))
		zw.Close()

	} else {
		body.Write([]byte(word))
	}

	res := fmt.Sprintf("HTTP/1.1 200 OK\r\n%sContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n", contentEncodingHeader, body.Len())

	writer.WriteString(res)
	writer.Write(body.Bytes())
}

// handleFileRequest will handle requests for files
func handleFileRequest(reader *bufio.Reader, writer *bufio.Writer, method string, lines []string, path string) {
	if len(os.Args) != 3 || os.Args[1] != "--directory" {
		fmt.Println("Flag --directory <directory> is required")
		os.Exit(1)
	}

	directory := os.Args[2]
	_, err := os.Stat(directory)
	if os.IsNotExist(err) {
		fmt.Println("Directory does not exist")
		os.Exit(1)
	}

	filePath := fmt.Sprintf("%s%s", directory, strings.TrimPrefix(path, "files/"))

	switch method {
	case "GET":
		file, err := os.Open(filePath)
		if err != nil {
			writer.WriteString(StatusNotFound)
			return
		}
		defer file.Close()

		fileInfo, err := file.Stat()
		if err != nil {
			writer.WriteString(StatusInternalServerError)
			return
		}

		res := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\n\r\n", fileInfo.Size())
		writer.WriteString(res)

		buffer := make([]byte, 4096)
		for {
			n, err := file.Read(buffer)
			if err != nil {
				break
			}
			writer.Write(buffer[:n])
		}
	case "POST":
		file, err := os.Create(filePath)
		if err != nil {
			writer.WriteString(StatusInternalServerError)
			return
		}
		defer file.Close()

		contentLengthHeader := ""
		for _, line := range lines {
			if strings.HasPrefix(line, "Content-Length: ") {
				contentLengthHeader = strings.TrimPrefix(line, "Content-Length: ")
				contentLengthHeader = strings.TrimSpace(contentLengthHeader)
				break
			}
		}

		if contentLengthHeader == "" {
			writer.WriteString(StatusBadRequest)
			return
		}

		contentLength, err := strconv.Atoi(contentLengthHeader)
		if err != nil {
			writer.WriteString(StatusBadRequest)
			return
		}

		if contentLength > 0 {
			buffer := make([]byte, 4096)
			remaining := contentLength
			for remaining > 0 {
				n, err := reader.Read(buffer)
				if err != nil && err != io.EOF {
					writer.WriteString(StatusInternalServerError)
					return
				}
				if n == 0 {
					break
				}

				if _, err := file.Write(buffer[:n]); err != nil {
					writer.WriteString(StatusInternalServerError)
					return
				}

				remaining -= n
			}
		}

		writer.WriteString(StatusCreated)
	}
}
