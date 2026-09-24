package alidns

import (
	"context"
	"fmt"
	"os"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/aliyun/credentials-go/credentials"
	api "github.com/go-acme/alidns-20150109/v4/client"
)

type Record struct {
	ProviderID string
	Host       string
	Type       string
	Value      string
	TTL        int
}

type Client struct {
	client *api.Client
	domain string
}

func New(domain string) (*Client, error) {
	role := getenv("ALICLOUD_RAM_ROLE", "resume-askcode")
	region := getenv("ALICLOUD_REGION_ID", "cn-hangzhou")
	cred, err := credentials.NewCredential(new(credentials.Config).SetType("ecs_ram_role").SetRoleName(role))
	if err != nil {
		return nil, fmt.Errorf("create ECS RAM credential: %w", err)
	}
	cfg := new(openapi.Config).SetRegionId(region).SetCredential(cred)
	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("create AliDNS client: %w", err)
	}
	return &Client{client: client, domain: domain}, nil
}

func (c *Client) List(ctx context.Context) ([]Record, error) {
	var out []Record
	for page := int64(1); ; page++ {
		request := new(api.DescribeDomainRecordsRequest).SetDomainName(c.domain).SetPageNumber(page).SetPageSize(500)
		response, err := api.DescribeDomainRecordsWithContext(ctx, c.client, request, &dara.RuntimeOptions{})
		if err != nil {
			return nil, fmt.Errorf("describe AliDNS records: %w", err)
		}
		if response.Body == nil || response.Body.DomainRecords == nil {
			return out, nil
		}
		for _, item := range response.Body.DomainRecords.Record {
			if item == nil {
				continue
			}
			out = append(out, Record{ProviderID: value(item.RecordId), Host: value(item.RR), Type: value(item.Type), Value: value(item.Value), TTL: int(number(item.TTL))})
		}
		if len(out) >= int(number(response.Body.TotalCount)) || len(response.Body.DomainRecords.Record) == 0 {
			return out, nil
		}
	}
}

func (c *Client) Add(ctx context.Context, record Record) (string, error) {
	request := new(api.AddDomainRecordRequest).SetDomainName(c.domain).SetRR(record.Host).SetType(record.Type).SetValue(record.Value).SetTTL(int64(record.TTL))
	response, err := api.AddDomainRecordWithContext(ctx, c.client, request, &dara.RuntimeOptions{})
	if err != nil {
		return "", fmt.Errorf("add AliDNS record: %w", err)
	}
	if response == nil || response.Body == nil || response.Body.RecordId == nil {
		return "", fmt.Errorf("AliDNS returned no record id")
	}
	return *response.Body.RecordId, nil
}

func (c *Client) Delete(ctx context.Context, providerID string) error {
	if providerID == "" {
		return fmt.Errorf("AliDNS record id is empty")
	}
	_, err := api.DeleteDomainRecordWithContext(ctx, c.client, new(api.DeleteDomainRecordRequest).SetRecordId(providerID), &dara.RuntimeOptions{})
	if err != nil {
		return fmt.Errorf("delete AliDNS record: %w", err)
	}
	return nil
}

func value(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func number(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
