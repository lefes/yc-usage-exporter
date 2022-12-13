// TODO: Add proper logging
// TODO: Refactor error handling
// TODO: Separate functions to smaller ones for better reusability
// TODO: Refactor passing of structer []Folder to functions
// Maybe it's better to pass it as pointer?
// Maybe TODO: bind some of the functions to Folder struct?
// Maybe TODO: remove custom struct and use yandex-cloud structs?
// TODO: Add work with labels and tags on resources
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
	CloudName      string
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

func getFoldersList(sdk *ycsdk.SDK, ctx context.Context) ([]Folder, error) {
	var clouds []*resourcemanager.Cloud
	cloudList, err := sdk.ResourceManager().Cloud().List(ctx, &resourcemanager.ListCloudsRequest{})
	if err != nil {
		return nil, err
	}
	clouds = append(clouds, cloudList.Clouds...)
	for cloudList.NextPageToken != "" {
		cloudList, err = sdk.ResourceManager().Cloud().List(ctx, &resourcemanager.ListCloudsRequest{
			PageToken: cloudList.NextPageToken,
		})
		if err != nil {
			return nil, err
		}
		clouds = append(clouds, cloudList.Clouds...)
	}
	folders := make([]Folder, 0)
	for _, cloud := range clouds {
		var cloudFolders []*resourcemanager.Folder
		folderList, err := sdk.ResourceManager().Folder().List(ctx, &resourcemanager.ListFoldersRequest{CloudId: cloud.Id})
		if err != nil {
			return nil, err
		}
		cloudFolders = append(cloudFolders, folderList.Folders...)
		for folderList.NextPageToken != "" {
			folderList, err = sdk.ResourceManager().Folder().List(ctx, &resourcemanager.ListFoldersRequest{
				CloudId:   cloud.Id,
				PageToken: folderList.NextPageToken,
			})
			if err != nil {
				return nil, err
			}
			cloudFolders = append(cloudFolders, folderList.Folders...)
		}

		for _, folder := range cloudFolders {
			folders = append(folders, Folder{
				CloudName: cloud.Name,
				Name:      folder.Name,
				Id:        folder.Id,
			})
		}

	}
	return folders, nil
}

// TODO: Add goroutines with throttling and dynamic number of goroutines with default value
func getComputeResources(sdk *ycsdk.SDK, ctx context.Context, folders []Folder) ([]Folder, error) {
	for i, folder := range folders {
		actualFolder := &folders[i]
		var instances []*compute.Instance
		computeResources, err := sdk.Compute().Instance().List(ctx, &compute.ListInstancesRequest{FolderId: folder.Id})
		if err != nil {
			return nil, err
		}
		instances = append(instances, computeResources.Instances...)
		for computeResources.NextPageToken != "" {
			computeResources, err = sdk.Compute().Instance().List(ctx, &compute.ListInstancesRequest{
				FolderId:  folder.Id,
				PageToken: computeResources.NextPageToken,
				PageSize:  1000,
			})
			if err != nil {
				return nil, err
			}
			instances = append(instances, computeResources.Instances...)
		}

		for _, computeResource := range instances {
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
	err = w.Write([]string{"Cloud", "Folder", "CPU (cores)", "Memory (Gb)", "Disc (Gb)", "S3 (Gb)", "IPs"})
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
			folder.CloudName,
			folder.Name,
			strconv.Itoa(cpus),
			strconv.Itoa(memory),
			strconv.Itoa(disc),
			strconv.Itoa(folder.S3size),
			strconv.Itoa(folder.IpCount),
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
