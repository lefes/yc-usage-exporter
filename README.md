# Yandex Cloud usage exporter

## About the project

This project is a little tool to export Yandex Cloud usage data to CSV file. It is written in Go and uses Yandex Cloud SDK for Go.


### Input data
CLI tool accepts the following parameters:
  - token - Yandex Cloud OAuth token
  - output - path to output file 
<br>
CLI took automatically can get token from environment variable or from file with default config. To get token you need to create service account in Yandex Cloud and generate OAuth token for it. You can find more information about it [here](https://cloud.yandex.com/docs/iam/operations/sa/create-token).

### Output data
The tool exports the following data to CSV file:
  - Cloud Name
  - Folder Name
  - CPU Cores (vCPU)
  - RAM (GB)
  - Disk Space (GB)
  - S3 Storage (GB)
  - IP addresses (count)

## Getting started

### Prerequisites
  - Go 1.13 or higher
  - Yandex Cloud SDK for Go

### Installation

To install the tool you need to clone the repository and build it with Go:
```
git clone
cd yc-usage-exporter
go build
```

### Usage

To use the tool you need to get OAuth token for service account and run the tool with the following parameters:
```
./yc-usage-exporter -token <token> -output <output_file>
```

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details
