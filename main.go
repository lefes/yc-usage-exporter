// TODO: Add proper logging
// TODO: Refactor error handling
// TODO: Separate functions to smaller ones for better reusability
// TODO: Refactor passing of structer []Folder to functions
// Maybe it's better to pass it as pointer?
// Maybe TODO: bind some of the functions to Folder struct?
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"os"
	"strconv"

	"github.com/yandex-cloud/go-genproto/yandex/cloud/compute/v1"
	"github.com/yandex-cloud/go-genproto/yandex/cloud/resourcemanager/v1"
	"github.com/yandex-cloud/go-genproto/yandex/cloud/storage/v1"
	"github.com/yandex-cloud/go-genproto/yandex/cloud/vpc/v1"
	ycsdk "github.com/yandex-cloud/go-sdk"
	"gopkg.in/yaml.v2"
)

type Response struct {
	StatusCode int         `json:"statusCode"`
	Body       interface{} `json:"body"`
}

type YandexCreds struct {
	Profiles struct {
		Default struct {
			Token string `yaml:"token"`
		} `yaml:"default"`
	} `yaml:"profiles"`
}

type Cloud struct {
	Name    string
	Id      string
	Folders []Folder
}

type Folder struct {
	Name           string
	Id             string
	Instances      []Instance `json:"instances"`
	S3size         int
	IpCount        int
	InternetEgress int
}

type Instance struct {
	Name     string
	CPU      int
	Memory   int
	Fraction int
	Disks    []Disk
}

type Disk struct {
	Name string
	Size int
}

func getToken() string {
	var token string
	flag.StringVar(&token, "token", "",
		"Yandex Cloud token")
	flag.Parse()
	if token != "" {
		return token
	}

	token = os.Getenv("YANDEX_CLOUD_TOKEN")
	if token != "" {
		return token
	}

	var creds YandexCreds
	homeDir, _ := os.UserHomeDir()
	credsFile, err := os.ReadFile(homeDir + "/" + ".config/yandex-cloud/config.yaml")
	if err != nil {
		panic(err)
	}
	err = yaml.Unmarshal(credsFile, &creds)
	if err != nil {
		panic(err)
	}
	if creds.Profiles.Default.Token == "" {
		panic("No token found")
	}
	return creds.Profiles.Default.Token
}

// TODO: Add pagination support
// TODO: Add support to muiltiple clouds
func getFoldersList(sdk *ycsdk.SDK, ctx context.Context) ([]Folder, error) {
	clouds, err := sdk.ResourceManager().Cloud().List(ctx, &resourcemanager.ListCloudsRequest{})
	if err != nil {
		return nil, err
	}
	folders := make([]Folder, 0)
	for _, cloud := range clouds.Clouds {
		cloudFolders, err := sdk.ResourceManager().Folder().List(ctx, &resourcemanager.ListFoldersRequest{CloudId: cloud.Id})
		if err != nil {
			return nil, err
		}
		for _, folder := range cloudFolders.Folders {

			folders = append(folders, Folder{
				Name: folder.Name,
				Id:   folder.Id,
			})
		}

	}
	return folders, nil
}

// TODO: Add pagination support
// TODO: Add goroutines with throttling and dynamic number of goroutines with default value
func getComputeResources(sdk *ycsdk.SDK, ctx context.Context, folders []Folder) ([]Folder, error) {
	for i, folder := range folders {
		actualFolder := &folders[i]
		computeResources, err := sdk.Compute().Instance().List(ctx, &compute.ListInstancesRequest{FolderId: folder.Id})
		if err != nil {
			return nil, err
		}
		for _, computeResource := range computeResources.Instances {
			instance := Instance{
				Name:     computeResource.Name,
				CPU:      int(computeResource.Resources.Cores),
				Memory:   int(computeResource.Resources.Memory),
				Fraction: int(computeResource.Resources.CoreFraction),
			}
			// Getting boot disk size
			bootDisk, err := sdk.Compute().Disk().Get(ctx, &compute.GetDiskRequest{DiskId: computeResource.BootDisk.DiskId})
			if err != nil {
				return nil, err
			}
			instance.Disks = append(instance.Disks, Disk{
				Name: bootDisk.Name,
				Size: int(bootDisk.Size),
			})
			// Getting Secondary disks size
			for _, disk := range computeResource.SecondaryDisks {
				secondaryDisk, err := sdk.Compute().Disk().Get(ctx, &compute.GetDiskRequest{DiskId: disk.DiskId})
				if err != nil {
					return nil, err
				}
				instance.Disks = append(instance.Disks, Disk{
					Name: secondaryDisk.Name,
					Size: int(secondaryDisk.Size),
				})
			}
			actualFolder.Instances = append(actualFolder.Instances, instance)
		}
	}
	return folders, nil
}

// TODO: get out calculation of resources to separate function
// TODO: Add option to set output file name
// TODO: Add support of multiple clouds
func exportToCSV(resources []Folder) {
	f, err := os.Create("instances.csv")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	err = w.Write([]string{"Folder", "CPU", "Memory", "Disc"})
	if err != nil {
		panic(err)
	}
	for _, folder := range resources {
		cpus := 0
		memory := 0
		disc := 0
		for _, instance := range folder.Instances {
			cpus += instance.CPU * (instance.Fraction / 100)
			memory += instance.Memory / (1 << 30)
			for _, disk := range instance.Disks {
				disc += disk.Size / (1 << 30)
			}
		}
		err := w.Write([]string{
			folder.Name,
			strconv.Itoa(cpus),
			strconv.Itoa(memory),
			strconv.Itoa(disc),
		})
		if err != nil {
			panic(err)
		}
	}
}

func getS3size(sdk *ycsdk.SDK, ctx context.Context, folders []Folder) ([]Folder, error) {
	for i, folder := range folders {
		actualFolder := &folders[i]
		s3, err := sdk.StorageAPI().Bucket().List(ctx, &storage.ListBucketsRequest{FolderId: folder.Id})
		if err != nil {
			return nil, err
		}
		for _, bucket := range s3.Buckets {
			size, err := sdk.StorageAPI().Bucket().GetStats(ctx, &storage.GetBucketStatsRequest{Name: bucket.Name})
			if err != nil {
				return nil, err
			}
			actualFolder.S3size += int(size.UsedSize / (1 << 30))
		}
	}
	return folders, nil
}

func getNetworkstats(sdk *ycsdk.SDK, ctx context.Context, folders []Folder) ([]Folder, error) {
	for i, folder := range folders {
		actualFolder := &folders[i]
		networks, err := sdk.VPC().Address().List(ctx, &vpc.ListAddressesRequest{FolderId: folder.Id})
		if err != nil {
			return nil, err
		}
		actualFolder.IpCount = len(networks.Addresses)
	}

	return folders, nil
}

func main() {
	ctx := context.Background()
	token := getToken()
	sdk, err := ycsdk.Build(ctx, ycsdk.Config{
		Credentials: ycsdk.OAuthToken(token),
	})
	if err != nil {
		panic(err)
	}
	folders, err := getFoldersList(sdk, ctx)
	if err != nil {
		panic(err)
	}
	computeResources, err := getComputeResources(sdk, ctx, folders)
	if err != nil {
		panic(err)
	}
	computeResources, err = getS3size(sdk, ctx, computeResources)
	if err != nil {
		panic(err)
	}
	computeResources, err = getNetworkstats(sdk, ctx, computeResources)
	if err != nil {
		panic(err)
	}

	exportToCSV(computeResources)

}
