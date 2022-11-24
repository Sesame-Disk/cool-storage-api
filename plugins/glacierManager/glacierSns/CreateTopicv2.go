package glaciersns

import (
	"context"
	"cool-storage-api/configread"
	"cool-storage-api/plugins/awsAuth"
	"flag"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/service/sns"
)

// SNSCreateTopicAPI defines the interface for the CreateTopic function.
// We use this interface to test the function using a mocked service.
type SNSCreateTopicAPI interface {
	CreateTopic(ctx context.Context,
		params *sns.CreateTopicInput,
		optFns ...func(*sns.Options)) (*sns.CreateTopicOutput, error)
}

// MakeTopic creates an Amazon Simple Notification Service (Amazon SNS) topic.
// Inputs:
//     c is the context of the method call, which includes the AWS Region.
//     api is the interface that defines the method call.
//     input defines the input arguments to the service call.
// Output:
//     If success, a CreateTopicOutput object containing the result of the service call and nil.
//     Otherwise, nil and an error from the call to CreateTopic.
func MakeTopic(c context.Context, api SNSCreateTopicAPI, input *sns.CreateTopicInput) (*sns.CreateTopicOutput, error) {
	return api.CreateTopic(c, input)
}

func CreateSNSTopic() {
	awsConfig := configread.Configuration.AWSConfig
	topic := flag.String("t", awsConfig.SNSTopicName, "The name of the topic")
	flag.Parse()

	if *topic == "" {
		fmt.Println("You must supply the name of the topic")
		fmt.Println("-t TOPIC")
		return
	}

	cfg, err := awsAuth.Authenticate()
	if err != nil {
		log.Fatalf("failed to load AWS configuration, %v", err)
	}

	client := sns.NewFromConfig(cfg)

	input := &sns.CreateTopicInput{
		Name: topic,
	}

	results, err := MakeTopic(context.TODO(), client, input)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	fmt.Println(*results.TopicArn)
}
