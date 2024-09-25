package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ses"
)

type Product struct {
	ID string `json:"id"`
}

type Response struct {
	Message  string    `json:"message"`
	Products []Product `json:"products`
}

const GEAR_SHOP_HOSTNAME = "https://rivian.com"

var sesClient *ses.SES

func init() {
	var aws_session *session.Session
	if os.Getenv("ENVIRONMENT") == "local" || os.Getenv("AWS_SAM_LOCAL") != "" {
		aws_session = session.Must(session.NewSession(&aws.Config{
			Endpoint: aws.String("http://localhost:8000"),
			Region:   aws.String("us-west-1"), // Use any valid region
		}))
	} else {
		aws_session = session.Must(session.NewSession())
	}
	sesClient = ses.New(aws_session)
}

// Handler function for AWS Lambda
func Handler(ctx context.Context, event events.DynamoDBEvent) (Response, error) {
	var products []Product

	for _, record := range event.Records {
		// log.Printf("Processing event received from dynamodb stream: %v", record)
		// log.Printf("change: %v", record.Change)
		// log.Printf("change.NewImage: %v", record.Change.NewImage)

		fmt.Printf("DynamoDB Event Received with EventName : %s , Change : %v", record.EventName, record.Change)

		// new product discovered
		if record.EventName == "INSERT" {
			id := record.Change.NewImage["Id"].String()
			name := record.Change.NewImage["Name"].String()
			price := record.Change.NewImage["Price"].String()
			url := record.Change.NewImage["GearShopUrl"].String()

			if id == "" && name == "" && url == "" {
				fmt.Printf("failed to parse record change event : %v", record.Change.NewImage)
				break
			}

			products = append(products, Product{ID: id}) // Add product ID to the list

			message := fmt.Sprintf(
				"New Rivian Gear Shop product added: %s, Url: %s%s, Price: %s",
				name, GEAR_SHOP_HOSTNAME, url, price,
			)
			fmt.Println(message)

			if os.Getenv("ENVIRONMENT") == "local" || os.Getenv("AWS_SAM_LOCAL") != "" {
				fmt.Printf("**skipping publishing to SNS locally for now**")
				break
			}

			sourceEmail := fmt.Sprintf("Rivian GearShop Notifier <%s>", os.Getenv("SOURCE_EMAIL"))
			sourceArn := os.Getenv("SOURCE_ARN")
			replyToAddresses := strings.Split(os.Getenv("REPLY_TO_ADDRESSES"), ",")
			bccAddresses := strings.Split(os.Getenv("BCC_ADDRESSES"), ",")

			sendEmailInput := &ses.SendEmailInput{
				Source:           aws.String(sourceEmail),
				SourceArn:        aws.String(sourceArn),
				ReplyToAddresses: aws.StringSlice(replyToAddresses),
				Destination: &ses.Destination{
					BccAddresses: aws.StringSlice(bccAddresses),
				},
				Message: &ses.Message{
					Subject: &ses.Content{
						Data: aws.String("[Rivian GearShop] New Product Alert"),
					},
					Body: &ses.Body{
						Text: &ses.Content{
							Data: aws.String(message),
						},
					},
				},
			}
			result, err := sesClient.SendEmail(sendEmailInput)
			if err != nil {
				return Response{}, fmt.Errorf("failed ses:SendEmailInput request: %v", err)
			}

			fmt.Printf("Successfully sent SES email: %s with MessageID: %s", message, *result.MessageId)
		}
	}

	response := Response{
		Message:  fmt.Sprintf("Successfully processed %d DynamoDB stream event(s)", len(event.Records)),
		Products: products,
	}
	return response, nil
}

func main() {
	lambda.Start(Handler)
}
