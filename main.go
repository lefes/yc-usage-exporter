package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cheggaaa/pb/v3"
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

func parsingArgs() (string, string, error) {
	var token string
	var outputFileName string
	// Parsing args
	flag.StringVar(&token, "token", "", "Yandex cloud token")
	flag.StringVar(&outputFileName, "output", "", "Output file name")
	flag.Parse()

	// validating that path is safe
	if outputFileName == "" {
		outputFileName = "instances.csv"
	}
	outputFileName = filepath.Clean(outputFileName)
	if strings.HasPrefix(outputFileName, "/") {
		return "", "", fmt.Errorf("parsingArgs: output file name should not start with /")
	}
	if token != "" {
		return token, outputFileName, nil
	}

	// Parsing env
	token = os.Getenv("YANDEX_CLOUD_TOKEN")
	if token != "" {
		return token, outputFileName, nil
	}

	// Parsing config file
	var creds YandexCreds
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("parsingArgs: error while getting home dir: %s", err)
	}
	homeDir = filepath.Clean(homeDir)
	configPath := homeDir + "/" + ".config/yandex-cloud/config.yaml"
	if _, err = os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
		//#nosec G304
		credsFile, err := os.ReadFile(configPath)
		if err != nil {
			return "", "", fmt.Errorf("parsingArgs: error while reading yandex config file: %s", err)
		}
		err = yaml.Unmarshal(credsFile, &creds)
		if err != nil {
			return "", "", fmt.Errorf("parsingArgs: error while parsing yandex config file: %s", err)
		}
	}
	if err != nil {
		return "", "", fmt.Errorf("parsingArgs: error while getting yandex config file: %s", err)
	}

	if creds.Profiles.Default.Token == "" {
		return "", "", fmt.Errorf("parsingArgs: no token found in config file")
	}
	log.Printf("Token found")
	log.Printf("Output file name: %s", outputFileName)
	return creds.Profiles.Default.Token, outputFileName, nil
}

func workerGroup(
	sdk *ycsdk.SDK,
	ctx context.Context,
	folders []Folder,
	calculateFunction func(folder *Folder, sdk *ycsdk.SDK, ctx context.Context, mu *sync.RWMutex) error,
) ([]Folder, error) {
	log.Print("Starting workers...")
	count := len(folders)
	var wg sync.WaitGroup
	var mu sync.RWMutex
	const workers = 10
	foldersChannel := make(chan *Folder, workers)
	bar := pb.StartNew(count)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(
			id int,
			foldersChannel <-chan *Folder,
			sdk *ycsdk.SDK,
			ctx context.Context,
			mu *sync.RWMutex,
			calculateFunction func(folder *Folder, sdk *ycsdk.SDK, ctx context.Context, mu *sync.RWMutex) error,
		) {
			for folder := range foldersChannel {
				err := calculateFunction(folder, sdk, ctx, mu)
				if err != nil {
					log.Printf("Error while calculating folder %s: %s", folder.Name, err)
				}
				mu.Lock()
				bar.Increment()
				mu.Unlock()
			}
			wg.Done()
		}(i, foldersChannel, sdk, ctx, &mu, calculateFunction)
	}

	for i := range folders {
		foldersChannel <- &folders[i]
	}

	close(foldersChannel)
	wg.Wait()
	bar.Finish()
	return folders, nil
}

func getCloudList(sdk *ycsdk.SDK, ctx context.Context) ([]Cloud, error) {
	log.Print("Getting cloud list...")
	var clouds []*resourcemanager.Cloud

	cloudList, err := sdk.ResourceManager().Cloud().List(ctx, &resourcemanager.ListCloudsRequest{})
	if err != nil {
		return nil, fmt.Errorf("getCloudList error while getting cloud list: %s", err)
	}
	clouds = append(clouds, cloudList.Clouds...)

	for cloudList.NextPageToken != "" {
		cloudList, err = sdk.ResourceManager().Cloud().List(ctx, &resourcemanager.ListCloudsRequest{
			PageToken: cloudList.NextPageToken,
		})
		if err != nil {
			return nil, fmt.Errorf("getCloudList: error while getting cloud list with NextPageToken: %s", err)
		}
		clouds = append(clouds, cloudList.Clouds...)
	}

	log.Printf("Found %d clouds", len(clouds))

	cloudsList := make([]Cloud, 0)
	for _, cloud := range clouds {
		cloudsList = append(cloudsList, Cloud{
			Name: cloud.Name,
			Id:   cloud.Id,
		})
	}

	return cloudsList, nil
}

func getFoldersList(sdk *ycsdk.SDK, ctx context.Context) ([]Folder, error) {
	clouds, err := getCloudList(sdk, ctx)
	if err != nil {
		return nil, fmt.Errorf("getFoldersList: error while getting cloud list: %s", err)
	}

	log.Print("Getting folders list...")

	folders := make([]Folder, 0)
	for _, cloud := range clouds {
		var cloudFolders []*resourcemanager.Folder
		folderList, err := sdk.ResourceManager().Folder().List(ctx, &resourcemanager.ListFoldersRequest{CloudId: cloud.Id})
		if err != nil {
			return nil, fmt.Errorf("getFoldersList: error while getting folder list for cloud %s: %s", cloud.Name, err)
		}

		cloudFolders = append(cloudFolders, folderList.Folders...)
		for folderList.NextPageToken != "" {
			folderList, err = sdk.ResourceManager().Folder().List(ctx, &resourcemanager.ListFoldersRequest{
				CloudId:   cloud.Id,
				PageToken: folderList.NextPageToken,
			})
			if err != nil {
				return nil, fmt.Errorf("getFoldersList: error while getting folder list for cloud %s with NextPageToken: %s", cloud.Name, err)
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

	log.Printf("Found %d folders", len(folders))
	return folders, nil
}

func calculateComputeResources(folder *Folder, sdk *ycsdk.SDK, ctx context.Context, mu *sync.RWMutex) error {
	var instances []*compute.Instance

	computeResources, err := sdk.Compute().Instance().List(ctx, &compute.ListInstancesRequest{FolderId: folder.Id})
	if err != nil {
		fmt.Printf("Error while getting compute resources for folder %s: %s", folder.Name, err)
	}
	instances = append(instances, computeResources.Instances...)

	for computeResources.NextPageToken != "" {
		computeResources, err = sdk.Compute().Instance().List(ctx, &compute.ListInstancesRequest{
			FolderId:  folder.Id,
			PageToken: computeResources.NextPageToken,
			PageSize:  1000,
		})
		if err != nil {
			fmt.Printf("Error while getting compute resources for folder %s with NextPageToken: %s", folder.Name, err)
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
			fmt.Printf("Error while getting boot disk for instance %s: %s", computeResource.Name, err)
		}
		instance.Disks = append(instance.Disks, Disk{
			Name: bootDisk.Name,
			Size: int(bootDisk.Size),
		})

		// Getting Secondary disks size
		for _, disk := range computeResource.SecondaryDisks {
			secondaryDisk, err := sdk.Compute().Disk().Get(ctx, &compute.GetDiskRequest{DiskId: disk.DiskId})
			if err != nil {
				fmt.Printf("Error while getting secondary disk for instance %s: %s", computeResource.Name, err)
			}
			instance.Disks = append(instance.Disks, Disk{
				Name: secondaryDisk.Name,
				Size: int(secondaryDisk.Size),
			})
		}

		mu.Lock()
		folder.Instances = append(folder.Instances, instance)
		mu.Unlock()
	}
	return nil
}

func calculateS3size(folder *Folder, sdk *ycsdk.SDK, ctx context.Context, mu *sync.RWMutex) error {
	s3, err := sdk.StorageAPI().Bucket().List(ctx, &storage.ListBucketsRequest{FolderId: folder.Id})
	if err != nil {
		fmt.Printf("Error while getting S3 buckets for folder %s: %s", folder.Name, err)
	}

	for _, bucket := range s3.Buckets {
		size, err := sdk.StorageAPI().Bucket().GetStats(ctx, &storage.GetBucketStatsRequest{Name: bucket.Name})
		if err != nil {
			fmt.Printf("Error while getting S3 bucket %s size: %s", bucket.Name, err)
		}
		mu.Lock()
		folder.S3size += int(size.UsedSize / (1 << 30))
		mu.Unlock()
	}
	return nil
}

func calculateNetworkstats(folder *Folder, sdk *ycsdk.SDK, ctx context.Context, mu *sync.RWMutex) error {
	networks, err := sdk.VPC().Address().List(ctx, &vpc.ListAddressesRequest{FolderId: folder.Id})
	if err != nil {
		fmt.Printf("Error while getting network resources for folder %s: %s", folder.Name, err)
	}

	mu.Lock()
	folder.IpCount = len(networks.Addresses)
	mu.Unlock()

	return nil
}

func exportToCSV(resources []Folder, outputFileName string) error {
	//#nosec G304
	f, err := os.Create(outputFileName)
	if err != nil {
		return fmt.Errorf("error while creating file %s: %s", outputFileName, err)
	}

	w := csv.NewWriter(f)
	defer w.Flush()
	err = w.Write([]string{"Cloud", "Folder", "CPU (cores)", "Memory (Gb)", "Disc (Gb)", "S3 (Gb)", "IPs"})
	if err != nil {
		return fmt.Errorf("error while writing to file %s: %s", outputFileName, err)
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
			return fmt.Errorf("error while writing to file %s data for folder %s: %s", outputFileName, folder.Name, err)
		}
	}
	err = f.Close()
	if err != nil {
		return fmt.Errorf("error while closing file %s: %s", outputFileName, err)
	}
	return nil
}

func main() {
	log.Print("Starting...")

	t := time.Now()
	defer func() {
		log.Printf("Done in %s", time.Since(t))
	}()

	log.Print("Parsing args...")
	token, outputFileName, err := parsingArgs()
	if err != nil {
		fmt.Printf("Error while parsing args: %s", err)
		return
	}

	log.Print("Building sdk...")
	ctx := context.Background()
	sdk, err := ycsdk.Build(ctx, ycsdk.Config{
		Credentials: ycsdk.OAuthToken(token),
	})
	if err != nil {
		fmt.Printf("Error while building sdk: %s", err)
		return
	}

	log.Print("Getting folders...")
	folders, err := getFoldersList(sdk, ctx)
	if err != nil {
		fmt.Printf("Error while getting folders list: %s", err)
		return
	}

	log.Print("Getting compute resources...")
	computeResources, err := workerGroup(sdk, ctx, folders, calculateComputeResources)
	if err != nil {
		fmt.Printf("Error while getting compute resources: %s", err)
		return
	}

	log.Print("Getting S3 size...")
	computeResources, err = workerGroup(sdk, ctx, computeResources, calculateS3size)
	if err != nil {
		fmt.Printf("Error while getting S3 size: %s", err)
		return
	}

	log.Print("Getting network stats...")
	computeResources, err = workerGroup(sdk, ctx, computeResources, calculateNetworkstats)
	if err != nil {
		fmt.Printf("Error while getting network stats: %s", err)
		return
	}

	log.Print("Exporting to csv...")
	err = exportToCSV(computeResources, outputFileName)
	if err != nil {
		fmt.Printf("Error while exporting to csv: %s", err)
		return
	}
}
