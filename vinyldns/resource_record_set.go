package vinyldns

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/terraform/helper/hashcode"
	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/vinyldns/go-vinyldns/vinyldns"
)

func resourceVinylDNSRecordSet() *schema.Resource {
	return &schema.Resource{
		Create: resourceVinylDNSRecordSetCreate,
		Read:   resourceVinylDNSRecordSetRead,
		Update: resourceVinylDNSRecordSetUpdate,
		Delete: resourceVinylDNSRecordSetDelete,

		Schema: map[string]*schema.Schema{
			"name": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
			},
			"zone_id": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
			},
			"type": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
			},
			"ttl": &schema.Schema{
				Type:     schema.TypeInt,
				Optional: true,
			},
			"account": &schema.Schema{
				Type:     schema.TypeString,
				Computed: true,
			},
			"record_addresses": &schema.Schema{
				Type:     schema.TypeSet,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Set: func(v interface{}) int {
					return hashcode.String(v.(string))
				},
			},
			"record_nsdnames": &schema.Schema{
				Type:     schema.TypeSet,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Set: func(v interface{}) int {
					return hashcode.String(v.(string))
				},
			},
			"record_cname": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
			},
			"record_text": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
			},
		},
	}
}

func resourceVinylDNSRecordSetCreate(d *schema.ResourceData, meta interface{}) error {
	name := d.Get("name").(string)
	log.Printf("[INFO] Creating vinyldns record set: %s", name)
	records, err := records(d)
	if err != nil {
		return err
	}
	created, err := meta.(*vinyldns.Client).RecordSetCreate(&vinyldns.RecordSet{
		Name:    d.Get("name").(string),
		ZoneID:  d.Get("zone_id").(string),
		Type:    d.Get("type").(string),
		TTL:     d.Get("ttl").(int),
		Records: records,
	})
	if err != nil {
		return err
	}

	d.SetId(created.RecordSet.ID)

	err = waitUntilRecordSetDeployed(d, meta, created.ChangeID)
	if err != nil {
		return err
	}

	return resourceVinylDNSRecordSetRead(d, meta)
}

func resourceVinylDNSRecordSetRead(d *schema.ResourceData, meta interface{}) error {
	log.Printf("[INFO] Reading vinyldns record set: %s", d.Id())
	rs, err := meta.(*vinyldns.Client).RecordSet(d.Get("zone_id").(string), d.Id())
	if err != nil {
		return err
	}

	d.Set("name", rs.Name)

	return nil
}

func resourceVinylDNSRecordSetUpdate(d *schema.ResourceData, meta interface{}) error {
	log.Printf("[INFO] Updating vinyldns record set: %s", d.Id())
	records, err := records(d)
	if err != nil {
		return err
	}
	updated, err := meta.(*vinyldns.Client).RecordSetUpdate(&vinyldns.RecordSet{
		Name:    d.Get("name").(string),
		ID:      d.Id(),
		ZoneID:  d.Get("zone_id").(string),
		Type:    d.Get("type").(string),
		TTL:     d.Get("ttl").(int),
		Records: records,
	})
	if err != nil {
		return err
	}

	err = waitUntilRecordSetDeployed(d, meta, updated.ChangeID)
	if err != nil {
		return err
	}

	return resourceVinylDNSRecordSetRead(d, meta)
}

func resourceVinylDNSRecordSetDelete(d *schema.ResourceData, meta interface{}) error {
	log.Printf("[INFO] Deleting vinyldns record set: %s", d.Id())

	deleted, err := meta.(*vinyldns.Client).RecordSetDelete(d.Get("zone_id").(string), d.Id())
	if err != nil {
		return err
	}

	err = waitUntilRecordSetDeployed(d, meta, deleted.ChangeID)
	if err != nil {
		return err
	}

	d.SetId("")

	return nil
}

func records(d *schema.ResourceData) ([]vinyldns.Record, error) {
	recordType := d.Get("type").(string)

	// SOA records are currently read-only and cannot be created, updated or deleted by vinyldns
	if recordType == "SOA" {
		return []vinyldns.Record{}, errors.New(recordType + " records are not currently supported by vinyldns")
	}

	if recordType == "CNAME" {
		cname := d.Get("record_cname").(string)

		if string(cname[len(cname)-1:]) != "." {
			return []vinyldns.Record{}, errors.New("record_cname must end in trailing '.'")
		}

		return []vinyldns.Record{
			vinyldns.Record{
				CName: cname,
			},
		}, nil
	}

	if recordType == "TXT" {
		return []vinyldns.Record{
			vinyldns.Record{
				Text: d.Get("record_text").(string),
			},
		}, nil
	}

	if recordType == "NS" {
		return nsRecordSets(stringSetToStringSlice(d.Get("record_nsdnames").(*schema.Set))), nil
	}

	return addressRecordSets(stringSetToStringSlice(d.Get("record_addresses").(*schema.Set))), nil
}

func addressRecordSets(addresses []string) []vinyldns.Record {
	records := []vinyldns.Record{}
	recordsCount := len(addresses)

	for i := 0; i < recordsCount; i++ {
		records = append(records, vinyldns.Record{
			Address: removeBrackets(addresses[i]),
		})
	}

	return records
}

func nsRecordSets(nsdnames []string) []vinyldns.Record {
	records := []vinyldns.Record{}
	recordsCount := len(nsdnames)

	for i := 0; i < recordsCount; i++ {
		records = append(records, vinyldns.Record{
			NSDName: nsdnames[i],
		})
	}

	return records
}

func stringSetToStringSlice(stringSet *schema.Set) []string {
	ret := []string{}
	if stringSet == nil {
		return ret
	}
	for _, envVal := range stringSet.List() {
		ret = append(ret, envVal.(string))
	}
	return ret
}

func waitUntilRecordSetDeployed(d *schema.ResourceData, meta interface{}, changeID string) error {
	stateConf := &resource.StateChangeConf{
		Pending:      []string{"Pending", ""},
		Target:       []string{"Complete"},
		Refresh:      recordSetStateRefreshFunc(d, meta, changeID),
		Timeout:      30 * time.Minute,
		Delay:        500 * time.Millisecond,
		MinTimeout:   15 * time.Second,
		PollInterval: 500 * time.Millisecond,
	}

	_, err := stateConf.WaitForState()
	return err
}

func recordSetStateRefreshFunc(d *schema.ResourceData, meta interface{}, changeID string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {
		log.Printf("[INFO] waiting for %v Complete status", d.Id())
		rsc, err := meta.(*vinyldns.Client).RecordSetChange(d.Get("zone_id").(string), d.Id(), changeID)
		if err != nil {
			if dErr, ok := err.(*vinyldns.Error); ok {
				if dErr.ResponseCode == http.StatusNotFound {
					return nil, "Pending", nil
				}

				log.Printf("[ERROR] %#v", err)
				return nil, "", err
			}

			log.Printf("[ERROR] %#v", err)
			return nil, "", err
		}

		if rsc.Status == "Failed" {
			err = errors.New("record set status Failed")
			log.Printf("[ERROR] record set status Failed: %#v", err)
			return rsc, rsc.Status, err
		}

		return rsc, rsc.Status, nil
	}
}

// vinyldns responds 400 to IPv6 addresses represented within `[` `]`
func removeBrackets(str string) string {
	return strings.Replace(strings.Replace(str, "[", "", -1), "]", "", -1)
}
