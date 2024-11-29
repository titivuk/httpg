package main

import (
	"bufio"
	"bytes"
	// "encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Request       = Request-Line
//                 *(( general-header
//                  | request-header
//                  | entity-header ) CRLF)
//                 CRLF
//                 [ message-body ]

// Request-Line   = Method SP Request-URI SP HTTP-Version CRLF

// Response = Status-Line               ;
//            *(( general-header        ;
//             | response-header        ;
//             | entity-header ) CRLF)  ;
//            CRLF
//            [ message-body ]          ;

// HTTP-Version SP Status-Code SP Reason-Phrase CRLF

// may be incomplete
var httpStatusCodes = map[int]string{
	100: "Continue",
	101: "Switching Protocols",
	102: "Processing",
	200: "OK",
	201: "Created",
	202: "Accepted",
	203: "Non-Authoritative Information",
	204: "No Content",
	205: "Reset Content",
	206: "Partial Content",
	207: "Multi-Status",
	208: "Already Reported",
	226: "IM Used",
	300: "Multiple Choices",
	301: "Moved Permanently",
	302: "Found",
	303: "See Other",
	304: "Not Modified",
	305: "Use Proxy",
	307: "Temporary Redirect",
	308: "Permanent Redirect",
	400: "Bad Request",
	401: "Unauthorized",
	402: "Payment Required",
	403: "Forbidden",
	404: "Not Found",
	405: "Method Not Allowed",
	406: "Not Acceptable",
	407: "Proxy Authentication Required",
	408: "Request Timeout",
	409: "Conflict",
	410: "Gone",
	411: "Length Required",
	412: "Precondition Failed",
	413: "Payload Too Large",
	414: "URI Too Long",
	415: "Unsupported Media Type",
	416: "Range Not Satisfiable",
	417: "Expectation Failed",
	418: "I'm a teapot",
	421: "Misdirected Request",
	422: "Unprocessable Entity",
	423: "Locked",
	424: "Failed Dependency",
	425: "Too Early",
	426: "Upgrade Required",
	428: "Precondition Required",
	429: "Too Many Requests",
	431: "Request Header Fields Too Large",
	451: "Unavailable For Legal Reasons",
	500: "Internal Server Error",
	501: "Not Implemented",
	502: "Bad Gateway",
	503: "Service Unavailable",
	504: "Gateway Timeout",
	505: "HTTP Version Not Supported",
	506: "Variant Also Negotiates",
	507: "Insufficient Storage",
	508: "Loop Detected",
	510: "Not Extended",
	511: "Network Authentication Required",
}

type Headers map[string][]string

func (h Headers) Add(key, value string) {
	v, ok := h[key]
	if !ok {
		h[key] = []string{value}
	} else {
		h[key] = append(v, value)
	}
}

func (h Headers) Set(key, value string) {
	h[key] = []string{value}
}

type Request struct {
	Method  string
	Url     *url.URL
	Proto   string
	Headers Headers
	Body    io.Reader
}

type Response struct {
	Headers    Headers
	statusCode int
	w          io.Writer
}

func (r *Response) Write(body []byte) (int, error) {
	statusCode := r.statusCode
	reason, ok := httpStatusCodes[statusCode]
	if !ok {
		statusCode = 200
		reason = httpStatusCodes[statusCode]
	}

	// status line
	n, err := r.w.Write([]byte(fmt.Sprintf("HTTP/1.1 %d %s \r\n", statusCode, reason)))
	if err != nil {
		return n, err
	}

	// headers
	if len(r.Headers) > 0 {
		for k, v := range r.Headers {
			if len(v) > 0 {
				var buf bytes.Buffer

				buf.WriteString(k)
				buf.WriteString(": ")

				buf.WriteString(v[0])
				for i := 1; i < len(v); i++ {
					buf.WriteByte(',')
					buf.WriteString(v[i])
				}

				buf.WriteString("\r\n")

				r.w.Write(buf.Bytes())
			}
		}
	}

	// headers / body delimeter
	n, err = r.w.Write([]byte("\r\n"))

	// body
	r.w.Write(body)

	return n, err
}

func (r *Response) StatusCode(code int) {
	r.statusCode = code
}

func main() {
	ln, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}

		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	_, err := parseRequest(conn)
	if err != nil {
		log.Fatal(err)
	}

	resp := Response{
		Headers: make(Headers),
		w:       conn,
	}

	resp.Headers.Set("Content-Type", "application/json")
	resp.Headers.Add("x-multi-header", "one")
	resp.Headers.Add("x-multi-header", "two")
	resp.StatusCode(201)
	_, err = resp.Write([]byte("hello"))
	if err != nil {
		log.Fatal(err)
	}
}

func parseRequest(r io.ReadCloser) (Request, error) {
	var req Request

	reader := bufio.NewReader(r)

	var l []byte

	// request line
	l, _, err := reader.ReadLine()
	if err != nil {
		return req, err
	}
	reqLineParts := strings.Split(string(l), " ")
	if len(reqLineParts) != 3 {
		return req, errors.New("invalid request line")
	}
	req.Method = reqLineParts[0]
	uri, err := url.Parse(reqLineParts[1])
	if err != nil {
		return req, errors.New("invalid request URI")
	}
	req.Url = uri
	req.Proto = reqLineParts[2]

	// headers
	// values with comma ',' as value is not supported
	// every commna ',' is treated as delimeter for multi-value header
	h := make(Headers)
	for {
		// TODO: handle long lines
		l, _, err = reader.ReadLine()
		if err != nil {
			return req, err
		}

		// CRLF line separating headers and body
		if len(l) == 0 {
			break
		}

		key, value := parseHeaderLine(string(l))
		h[key] = value
	}

	var contentLen uint64
	if clv, ok := h["Content-Length"]; ok {
		if len(clv) != 1 {
			return req, errors.New("content-length must have single value")
		}

		contentLen, err = strconv.ParseUint(clv[0], 10, 63)
		if err != nil {
			return req, err
		}

		if contentLen > 0 {
			req.Body = io.LimitReader(reader, int64(contentLen))
		} else {
			// should return EOF on the 1st read?
			req.Body = strings.NewReader("")
		}
	}

	return req, nil
}

func parseHeaderLine(l string) (string, []string) {
	parts := strings.SplitN(l, ":", 2)
	key := strings.TrimSpace(parts[0])
	values := strings.Split(strings.TrimSpace(parts[1]), ",")
	for i := 0; i < len(values); i++ {
		values[i] = strings.TrimSpace(values[i])
	}

	return key, values
}
