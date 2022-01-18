// Dreamhost DNS updater manages DNS host (A) records configured in Dreamhost,
// for the sake of a server being able to update its own public IP address.
// Will delete/overwrite any A records not pointing at the server's public IP.

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

const (
	DreamhostAPI = "https://api.dreamhost.com"
	WhatsmyipAPI = "https://myexternalip.com/raw"
)

var lastSuccessfulCheck = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "dreamhostcli",
	Name:      "last_successful_check",
	Help:      "The last time the DNS record was checked without an error",
})

func init() {
	prometheus.MustRegister(lastSuccessfulCheck)
}

func main() {
	apiKey := flag.String("api.key", "", "Dreamhost API token with permissions to change DNS records")
	dnsRecord := flag.String("dns.record", "", "DNS A record to update with our external IP")
	syncInterval := flag.Duration("sync.interval", 0, "frequency of DNS update eg: 15m (runs just once if left unset or 0)")
	dryRun := flag.Bool("dry-run", false, "don't actually change DNS records, just print the changes that would occur")
	flag.Parse()

	if *apiKey == "" {
		log.Fatal("required flag -api.key")
	}
	if *dnsRecord == "" {
		log.Fatal("required flag -dns.record")
	}
	if *syncInterval < 0 {
		log.Fatal("sync.interval must be >= 0")
	}

	cli := Client{
		Client:       http.Client{Timeout: 5 * time.Second},
		DreamhostTok: *apiKey,
		DryRun:       *dryRun,
	}

	if *syncInterval != 0 {
		http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "server is alive!")
		})
		http.Handle("/metrics", promhttp.Handler())
		log.Infof("running sync every %s", *syncInterval)
	}
	if err := cli.Run(*dnsRecord); err != nil {
		log.Fatal(err)
	} else {
		lastSuccessfulCheck.Set(float64(time.Now().Unix()))
	}

	if *syncInterval == 0 {
		log.Info("finished")
		os.Exit(0)
	}

	go func() {
		log.Fatal(http.ListenAndServe(":9000", nil))
	}()

	for range time.NewTicker(*syncInterval).C {
		if err := cli.Run(*dnsRecord); err != nil {
			log.Error(err)
		} else {
			lastSuccessfulCheck.Set(float64(time.Now().Unix()))
		}
	}
}

type Client struct {
	http.Client

	DreamhostTok string
	DryRun       bool
}

func (c Client) Run(record string) error {
	ip, err := c.ExtIP()
	if err != nil {
		return fmt.Errorf("checking our public IP address: %w", err)
	}
	log.Infof("our current public IP address: %s", ip)

	records, err := c.Records()
	if err != nil {
		return fmt.Errorf("getting dns records from dreamhost: %w", err)
	}

	var match *Record
	for _, r := range records {
		if r.Type == "A" && r.Record == record {
			match = r
			break
		}
	}

	if match != nil {
		// do nothing if record matches our public ip
		if match.Value == ip {
			log.Infof("nothing to do: record already points at our public IP %s", ip)
			return nil
		}

		// record does not match, must remove
		log.Infof("deleting record %s -> %s", match.Record, match.Value)
		if err := c.Delete(match.Record, match.Value); err != nil {
			return fmt.Errorf("deleting A record %s: %w", record, err)
		}
	}

	log.Infof("creating record %s -> %s", record, ip)
	if err := c.Create(record, ip); err != nil {
		return fmt.Errorf("creating A record %s -> %s: %w", record, ip, err)
	}
	return nil
}

type Record struct {
	Type   string `json:"type"`
	Record string `json:"record"`
	Value  string `json:"value"`
}

type Result struct {
	Result string `json:"result"`
	Data   string `json:"data"`
}

// Create creates an A record pointing at the given address.
func (c Client) Create(record string, address string) error {
	const url = "%s?format=json&cmd=dns-add_record&key=%s&type=A&record=%s&value=%s"

	if c.DryRun {
		log.Infof("dry-run: create A record %s -> %s", record, address)
		return nil
	}

	res, err := c.Get(fmt.Sprintf(url, DreamhostAPI, c.DreamhostTok, record, address))
	if err != nil {
		return fmt.Errorf("contacting api: %w", err)
	}
	defer res.Body.Close()

	var r Result
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return fmt.Errorf("unmarshaling response: %w", err)
	}

	if r.Result != "success" {
		return fmt.Errorf("record creation failed with %s: %s", r.Result, r.Data)
	}

	return nil
}

// Create deletes an A record for the given address.
func (c Client) Delete(record string, address string) error {
	url := fmt.Sprintf("%s?format=json&cmd=dns-remove_record&key=%s&type=A&record=%s&value=%s",
		DreamhostAPI, c.DreamhostTok, record, address)

	if c.DryRun {
		log.Infof("dry-run: delete A record %s -> %s", record, address)
		return nil
	}

	res, err := c.Get(url)
	if err != nil {
		return fmt.Errorf("contacting api: %w", err)
	}
	defer res.Body.Close()

	var r Result
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return fmt.Errorf("unmarshaling response: %w", err)
	}

	if r.Result != "success" {
		return fmt.Errorf("record removal failed with %s: %s", r.Result, r.Data)
	}
	return nil
}

// Records fetches the DNS records configured on the Dreamhost account.
func (c Client) Records() ([]*Record, error) {
	var r struct {
		Data   json.RawMessage `json:"data"`
		Result string          `json:"result"`
		Reason string          `json:"reason"`
	}
	url := fmt.Sprintf("%s?format=json&cmd=dns-list_records&key=%s", DreamhostAPI, c.DreamhostTok)

	res, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("unmarshaling json: %w", err)
	}

	if r.Result != "success" {
		return nil, errors.New(r.Reason)
	}

	var records []*Record
	if err := json.Unmarshal(r.Data, &records); err != nil {
		return nil, fmt.Errorf("unmarshalling json records: %w", err)
	}

	return records, nil
}

// ExtIP finds the public IP address of the network this script is running on.
func (c Client) ExtIP() (string, error) {
	res, err := c.Get(WhatsmyipAPI)
	if err != nil {
		return "", fmt.Errorf("contacting my-external-ip api: %w", err)
	}
	defer res.Body.Close()

	bs, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	ip := net.ParseIP(string(bs))
	if ip == nil {
		return "", fmt.Errorf("invalid ip address: %s", ip)
	}

	return ip.String(), nil
}
