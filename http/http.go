package http

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"
)

type responseBodyType string

type MultipartFormFieldType string

const (
	resBodyBytes responseBodyType = "bytes"
	resBodyNull  responseBodyType = "null"
	resBodyOther responseBodyType = "other"

	FieldType MultipartFormFieldType = "field"
	FileType  MultipartFormFieldType = "file"
)

var httpMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodPatch:   {},
	http.MethodDelete:  {},
	http.MethodConnect: {},
	http.MethodOptions: {},
	http.MethodTrace:   {},
}

type Null struct{}

type MultipartFormField struct {
	Type     MultipartFormFieldType
	Name     string
	Data     []byte
	FileName string
}

type Request[T any] struct {
	URL            *url.URL
	Method         string
	Headers        http.Header
	HeaderCallback func(http.Header) http.Header
	Body           any
	QueryParams    url.Values
	AcceptedStatus []int
	Timeout        *time.Duration
	Encoder        func(any) (io.Reader, error)
	Decoder        func(content []byte, statusCode int) (*T, error)
}

type Response[T any] struct {
	StatusCode int
	Body       []byte
	Headers    http.Header
	Data       *T
}

type ErrUnexpectedResponseStatus struct {
	StatusCode int
	Body       string
}

func (err *ErrUnexpectedResponseStatus) Error() string {
	if err.Body == "" {
		return fmt.Sprintf("unexpected status code: %d", err.StatusCode)
	} else {
		return fmt.Sprintf("unexpected status code: %d, response body: %s", err.StatusCode, err.Body)
	}
}

func DefaultJSONEncoder(v any) (io.Reader, error) {
	bs, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(bs), nil
}

func DefaultFormURLEncodedEncoder(v any) (io.Reader, error) {
	values, ok := v.(url.Values)
	if !ok {
		return nil, fmt.Errorf("form urlencoded encoder only supports url.Values type")
	}
	return strings.NewReader(values.Encode()), nil
}

// DefaultMultipartFormDataEncoder the returned headerCallback will set the Content-Type header with the boundary,
// and is required to be used together with the returned encoder.
func DefaultMultipartFormDataEncoder(boundary *string) (encoder func(any) (io.Reader, error),
	headerCallback func(header http.Header) http.Header) {
	encoder = func(v any) (io.Reader, error) {
		fields, ok := v.([]MultipartFormField)
		if !ok {
			return nil, fmt.Errorf("multipart form-data encoder only supports value of type []MultipartFormField")
		}

		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		if *boundary != "" {
			if err := writer.SetBoundary(*boundary); err != nil {
				return nil, err
			}
		}

		for i, field := range fields {
			if field.Name == "" {
				return nil, fmt.Errorf("name of the field of index %d cannot be empty", i)
			}

			if len(field.Data) == 0 {
				return nil, fmt.Errorf("data of field '%s' cannot be empty", field.Name)
			}

			if field.Type == FieldType {
				if err := writer.WriteField(field.Name, string(field.Data)); err != nil {
					return nil, err
				}
				continue
			}

			if field.Type == FileType {
				if field.FileName == "" {
					return nil, fmt.Errorf("file name of field '%s' cannot be empty", field.Name)
				}

				w, err := writer.CreateFormFile(field.Name, field.FileName)
				if err != nil {
					return nil, err
				}

				_, err = w.Write(field.Data)
				if err != nil {
					return nil, err
				}
			}
		}

		if *boundary == "" {
			*boundary = writer.Boundary()
		}

		if err := writer.Close(); err != nil {
			return nil, err
		}

		return &buf, nil
	}

	headerCallback = func(h http.Header) http.Header {
		h.Set("Content-Type", fmt.Sprintf("multipart/form-data;boundary=%s", *boundary))
		return h
	}

	return
}

func DefaultJSONDecoder[T any](content []byte, statusCode int) (*T, error) {
	switch statusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNonAuthoritativeInfo:
		var t T
		if err := json.Unmarshal(content, &t); err != nil {
			return nil, err
		}
		return &t, nil
	case http.StatusNoContent, http.StatusNotFound:
		return nil, nil
	default:
		return nil, &ErrUnexpectedResponseStatus{statusCode, string(content)}
	}
}

func DefaultXMLDecoder[T any](content []byte, statusCode int) (*T, error) {
	switch statusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNonAuthoritativeInfo:
		var t T
		if err := xml.Unmarshal(content, &t); err != nil {
			return nil, err
		}
		return &t, nil
	case http.StatusNoContent, http.StatusNotFound:
		return nil, nil
	default:
		return nil, &ErrUnexpectedResponseStatus{statusCode, string(content)}
	}
}

func (req *Request[T]) DoHttpRequest(ctx context.Context, client *http.Client) (*Response[T], error) {
	if client == nil {
		client = http.DefaultClient
	}

	if req.URL == nil {
		return nil, fmt.Errorf("request URL is not provided")
	}

	method := strings.ToUpper(req.Method)
	if _, ok := httpMethods[method]; !ok {
		return nil, fmt.Errorf("invalid HTTP method: %s", req.Method)
	}

	if len(req.AcceptedStatus) == 0 {
		req.AcceptedStatus = defaultAcceptedStatus(method)
	}

	var err error
	var reqBody io.Reader
	if canHaveRequestBody(method) && req.Body != nil {
		if body, ok := req.Body.(io.Reader); ok {
			reqBody = body
		} else if req.Encoder != nil {
			reqBody, err = req.Encoder(req.Body)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("request body encoder is not provided")
		}
	}

	var cancel context.CancelFunc
	if req.Timeout != nil {
		ctx, cancel = context.WithTimeout(ctx, *req.Timeout)
		defer cancel()
	}

	var resBodyType responseBodyType
	dataType := reflect.TypeFor[T]()
	switch dataType {
	case reflect.TypeOf(Null{}):
		resBodyType = resBodyNull
	case reflect.TypeOf([]byte(nil)):
		resBodyType = resBodyBytes
	default:
		resBodyType = resBodyOther
		if req.Decoder == nil {
			return nil, fmt.Errorf("response body decoder is not provided")
		}
	}

	if len(req.QueryParams) > 0 {
		req.URL.RawQuery = req.QueryParams.Encode()
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL.String(), reqBody)
	if err != nil {
		return nil, err
	}

	if len(req.Headers) > 0 {
		httpReq.Header = req.Headers
	}

	if req.HeaderCallback != nil {
		httpReq.Header = req.HeaderCallback(httpReq.Header)
	}

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	bs, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}

	if !slices.Contains(req.AcceptedStatus, httpResp.StatusCode) {
		return nil, &ErrUnexpectedResponseStatus{
			StatusCode: httpResp.StatusCode,
			Body:       string(bs),
		}
	}

	if resBodyType == resBodyBytes || resBodyType == resBodyNull {
		return &Response[T]{
			StatusCode: httpResp.StatusCode,
			Body:       bs,
			Headers:    httpResp.Header,
		}, nil
	}

	data, err := req.Decoder(bs, httpResp.StatusCode)
	if err != nil {
		return nil, err
	}

	return &Response[T]{
		StatusCode: httpResp.StatusCode,
		Body:       bs,
		Headers:    httpResp.Header,
		Data:       data,
	}, nil
}

func defaultAcceptedStatus(method string) []int {
	switch method {
	case http.MethodGet, http.MethodHead:
		return []int{http.StatusOK}
	case http.MethodPost:
		return []int{http.StatusOK, http.StatusCreated}
	case http.MethodPut:
		return []int{http.StatusOK, http.StatusCreated, http.StatusNoContent}
	case http.MethodDelete:
		return []int{http.StatusOK, http.StatusAccepted, http.StatusNoContent}
	case http.MethodPatch:
		return []int{http.StatusOK, http.StatusNoContent}
	case http.MethodConnect:
		return []int{
			http.StatusOK, http.StatusCreated, http.StatusAccepted,
			http.StatusNonAuthoritativeInfo, http.StatusNoContent,
			http.StatusResetContent, http.StatusPartialContent,
			http.StatusMultiStatus, http.StatusAlreadyReported,
			http.StatusIMUsed,
		}
	case http.MethodOptions:
		return []int{http.StatusOK, http.StatusNoContent}
	case http.MethodTrace:
		return []int{http.StatusOK}
	default:
		return []int{http.StatusOK}
	}
}

func canHaveRequestBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
