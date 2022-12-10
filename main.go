// TODO: Add proper logging
// TODO: Refactor error handling
// TODO: Separate functions to smaller ones for better reusability
package main

import (
	"context"
	"encoding/csv"
	"os"
	"strconv"

	"github.com/yandex-cloud/go-genproto/yandex/cloud/compute/v1"
	"github.com/yandex-cloud/go-genproto/yandex/cloud/resourcemanager/v1"
	"github.com/yandex-cloud/go-genproto/yandex/cloud/storage/v1"
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

// TODO: Add support of take token from env and pass it as argument
func getToken() string {
	var creds YandexCreds
	// Reading token from yaml file
	homeDir, _ := os.UserHomeDir()
	credsFile, err := os.ReadFile(homeDir + "/" + ".config/yandex-cloud/config.yaml")
	if err != nil {
		panic(err)
	}
	err = yaml.Unmarshal(credsFile, &creds)
	if err != nil {
		panic(err)
	}
	return creds.Profiles.Default.Token
}

// TODO: Add pagination support
// TODO: Add support to muiltiple clouds
func getFoldersList(ctx context.Context) ([]Folder, error) {
	token := getToken()
	sdk, err := ycsdk.Build(ctx, ycsdk.Config{
		Credentials: ycsdk.OAuthToken(token),
	})
	if err != nil {
		return nil, err
	}
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
func getComputeResources(ctx context.Context, folders []Folder) ([]Folder, error) {
	token := getToken()
	sdk, err := ycsdk.Build(ctx, ycsdk.Config{
		Credentials: ycsdk.OAuthToken(token),
	})
	if err != nil {
		return nil, err
	}
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
func exportToCSV(resources []Folder) {
	f, err := os.Create("instances.csv")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()

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
		err := w.Write([]string{folder.Name, "CPU - " + strconv.Itoa(cpus) + " шт\n" + "RAM - " + strconv.Itoa(memory) + " гб\n" + "Disk - " + strconv.Itoa(disc) + " гб\n" + "S3 - " + strconv.Itoa(folder.S3size) + " гб\n"})
		if err != nil {
			panic(err)
		}
	}
}

func getS3size(ctx context.Context, folders []Folder) ([]Folder, error) {
	token := getToken()
	sdk, err := ycsdk.Build(ctx, ycsdk.Config{
		Credentials: ycsdk.OAuthToken(token),
	})
	if err != nil {
		return nil, err
	}
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

func main() {
	ctx := context.Background()
	folders, err := getFoldersList(ctx)
	if err != nil {
		panic(err)
	}
	computeResources, err := getComputeResources(ctx, folders)
	if err != nil {
		panic(err)
	}
	computeResources, err = getS3size(ctx, computeResources)
	if err != nil {
		panic(err)
	}

	exportToCSV(computeResources)

}
