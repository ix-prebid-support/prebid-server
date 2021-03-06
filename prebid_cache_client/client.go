package prebid_cache_client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"

	"github.com/buger/jsonparser"
	"github.com/golang/glog"
	"github.com/prebid/prebid-server/config"
	"golang.org/x/net/context/ctxhttp"
)

// Client stores values in Prebid Cache. For more info, see https://github.com/prebid/prebid-cache
type Client interface {
	// PutJson stores JSON values for the given openrtb.Bids in the cache. Null values will be
	//
	// The returned string slice will always have the same number of elements as the values argument. If a
	// value could not be saved, the element will be an empty string. Implementations are responsible for
	// logging any relevant errors to the app logs
	PutJson(ctx context.Context, values []Cacheable) ([]string, []error)
}

type PayloadType string

const (
	TypeJSON PayloadType = "json"
	TypeXML  PayloadType = "xml"
)

type Cacheable struct {
	Type       PayloadType
	Data       json.RawMessage
	TTLSeconds int64
	Key        string
}

func NewClient(conf *config.Cache) Client {
	return &clientImpl{
		httpClient: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:    10,
				IdleConnTimeout: 65,
			},
		},
		putUrl: conf.GetBaseURL() + "/cache",
	}
}

type clientImpl struct {
	httpClient *http.Client
	putUrl     string
}

func (c *clientImpl) PutJson(ctx context.Context, values []Cacheable) (uuids []string, errs []error) {
	errs = make([]error, 0, 1)
	if len(values) < 1 {
		return nil, errs
	}

	uuidsToReturn := make([]string, len(values))

	postBody, err := encodeValues(values)
	if err != nil {
		glog.Errorf("Error creating JSON for prebid cache: %v", err)
		errs = append(errs, fmt.Errorf("Error creating JSON for prebid cache: %v", err))
		return uuidsToReturn, errs
	}
	httpReq, err := http.NewRequest("POST", c.putUrl, bytes.NewReader(postBody))
	if err != nil {
		glog.Errorf("Error creating POST request to prebid cache: %v", err)
		errs = append(errs, fmt.Errorf("Error creating POST request to prebid cache: %v", err))
		return uuidsToReturn, errs
	}
	httpReq.Header.Add("Content-Type", "application/json;charset=utf-8")
	httpReq.Header.Add("Accept", "application/json")

	anResp, err := ctxhttp.Do(ctx, c.httpClient, httpReq)
	if err != nil {
		glog.Errorf("Error sending the request to Prebid Cache: %v", err)
		errs = append(errs, fmt.Errorf("Error sending the request to Prebid Cache: %v", err))
		return uuidsToReturn, errs
	}
	defer anResp.Body.Close()

	responseBody, err := ioutil.ReadAll(anResp.Body)
	if anResp.StatusCode != 200 {
		glog.Errorf("Prebid Cache call to %s returned %d: %s", putURL, anResp.StatusCode, responseBody)
		errs = append(errs, fmt.Errorf("Prebid Cache call to %s returned %d: %s", putURL, anResp.StatusCode, responseBody))
		return uuidsToReturn, errs
	}

	currentIndex := 0
	processResponse := func(uuidObj []byte, dataType jsonparser.ValueType, offset int, err error) {
		if uuid, valueType, _, err := jsonparser.Get(uuidObj, "uuid"); err != nil {
			glog.Errorf("Prebid Cache returned a bad value at index %d. Error was: %v. Response body was: %s", currentIndex, err, string(responseBody))
			errs = append(errs, fmt.Errorf("Prebid Cache returned a bad value at index %d. Error was: %v. Response body was: %s", currentIndex, err, string(responseBody)))
		} else if valueType != jsonparser.String {
			glog.Errorf("Prebid Cache returned a %v at index %d in: %v", valueType, currentIndex, string(responseBody))
			errs = append(errs, fmt.Errorf("Prebid Cache returned a %v at index %d in: %v", valueType, currentIndex, string(responseBody)))
		} else {
			if uuidsToReturn[currentIndex], err = jsonparser.ParseString(uuid); err != nil {
				glog.Errorf("Prebid Cache response index %d could not be parsed as string: %v", currentIndex, err)
				errs = append(errs, fmt.Errorf("Prebid Cache response index %d could not be parsed as string: %v", currentIndex, err))
				uuidsToReturn[currentIndex] = ""
			}
		}
		currentIndex++
	}

	if _, err := jsonparser.ArrayEach(responseBody, processResponse, "responses"); err != nil {
		glog.Errorf("Error interpreting Prebid Cache response: %v\nResponse was: %s", err, string(responseBody))
		errs = append(errs, fmt.Errorf("Error interpreting Prebid Cache response: %v\nResponse was: %s", err, string(responseBody)))
		return uuidsToReturn, errs
	}

	return uuidsToReturn, errs
}

func encodeValues(values []Cacheable) ([]byte, error) {
	// This function assumes that m is non-nil and has at least one element.
	// clientImp.PutBids should respect this.
	var buf bytes.Buffer
	buf.WriteString(`{"puts":[`)
	for i := 0; i < len(values); i++ {
		if err := encodeValueToBuffer(values[i], i != 0, &buf); err != nil {
			return nil, err
		}
	}
	buf.WriteString("]}")
	return buf.Bytes(), nil
}

func encodeValueToBuffer(value Cacheable, leadingComma bool, buffer *bytes.Buffer) error {
	if leadingComma {
		buffer.WriteByte(',')
	}

	buffer.WriteString(`{"type":"`)
	buffer.WriteString(string(value.Type))
	if value.TTLSeconds > 0 {
		buffer.WriteString(`","ttlseconds":`)
		buffer.WriteString(strconv.FormatInt(value.TTLSeconds, 10))
		buffer.WriteString(`,"value":`)
	} else {
		buffer.WriteString(`","value":`)
	}
	buffer.Write(value.Data)
	if len(value.Key) > 0 {
		buffer.WriteString(`,"key":"`)
		buffer.WriteString(string(value.Key))
		buffer.WriteString(`"`)
	}
	buffer.WriteByte('}')
	return nil
}
