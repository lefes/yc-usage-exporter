package main

import (
	"context"
	"fmt"
	"os"

	"github.com/yandex-cloud/go-genproto/yandex/cloud/compute/v1"
	"github.com/yandex-cloud/go-genproto/yandex/cloud/resourcemanager/v1"
	ycsdk "github.com/yandex-cloud/go-sdk"
	"gopkg.in/yaml.v2"
)

type Request struct {
	FolderId string `json:"folderId"`
	Tag      string `json:"tag"`
}

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

func StartComputeInstances(ctx context.Context, request *Request) (*Response, error) {
	// Reading token from yaml file
	token := getToken()
	sdk, err := ycsdk.Build(ctx, ycsdk.Config{
		Credentials: ycsdk.OAuthToken(token),
	})
	if err != nil {
		return nil, err
	}
	listInstancesResponse, err := sdk.Compute().Instance().List(ctx, &compute.ListInstancesRequest{
		FolderId: request.FolderId,
	})
	if err != nil {
		return nil, err
	}
	instances := listInstancesResponse.GetInstances()
	count := 0
	for _, i := range instances {
		fmt.Printf("Starting instance %s", i.Name)
		count++
	}
	return &Response{
		StatusCode: 200,
		Body:       fmt.Sprintf("Started %d instances", count),
	}, nil
}

func getFoldersList(ctx context.Context) ([]string, error) {
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
	var folders []string
	for _, cloud := range clouds.Clouds {
		cloudFolders, err := sdk.ResourceManager().Folder().List(ctx, &resourcemanager.ListFoldersRequest{CloudId: cloud.Id})
		if err != nil {
			return nil, err
		}
		for _, folder := range cloudFolders.Folders {
			folders = append(folders, folder.Name)
		}
	}
	return folders, nil
}

func main() {
	ctx := context.Background()
	request := &Request{
		FolderId: "b1g9memkicue053heorv",
		Tag:      "test",
	}
	response, err := StartComputeInstances(ctx, request)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(response)
	fmt.Println(getFoldersList(ctx))
}
