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

// EmailTemplate generates an HTML email template for new product alerts.
func EmailTemplate(productName, productLink string, productImages []string, year int, referralCode string) string {
	var imagesHTML strings.Builder
	for _, img := range productImages {
		imagesHTML.WriteString(fmt.Sprintf(`<img src="%s" alt="%s" style="max-width:100%%; height:auto;"/>`, img, productName))
	}

	return fmt.Sprintf(`
	<!DOCTYPE html>
	<html lang="en">
	<head>
		<meta charset="UTF-8">
		<meta name="viewport" content="width=device-width, initial-scale=1.0">
		<title>New Gear Alert!</title>
		<style>
			body {
				font-family: 'Adventure','HelveticaNeue','Helvetica Neue',Helvetica,Arial,sans-serif;
				background-color: #f4f4f4;
				color: #333;
				margin: 0;
				padding: 20px;
			}
			.container {
				background-color: #ffffff;
				border-radius: 5px;
				box-shadow: 0 0 10px rgba(0,0,0,0.1);
				padding: 20px;
				max-width: 600px;
				margin: auto;
			}
			h1 {
				color: rgba(248,193,28,0.9);
			}
			a {
				color: #007bff;
				text-decoration: none;
			}
            .product-images {
                object-fit: cover;
                width: 100%%;
            }
            .product-images img {
                width: 45%%;
            }
			.footer {
				margin-top: 20px;
				font-size: 12px;
				color: #aaa;
				text-align: center;
			}
			.referral {
				background-color: rgba(248,193,28,0.9);
				font-family: 'Gtwalsheim', sans-serif;
                font-size: 15px;
                font-weight: 600;
                line-height: 1.3;
                padding: 10px;
                text-align: center;
			}
			.referral a {
                color: #000000 !important;
			}
		</style>
	</head>
	<body>
		<div class="container">
			<h1>New Product Alert!</h1>
			<p>We're excited to announce a new product has been seen in the Rivian Gear Shop:</p>
			<h2>%s</h2>
			<p>Check it out <a href="%s">here</a>.</p>
			<!-- Uncomment the following section to include images when ready -->
			<!-- <div class="product-images">%s</div> -->
			<div class="footer">
				<p>Thank you for being a part of the Rivian community!</p>
				<p>Check out the <a href="https://github.com/tmack8001/rivian-gear-shop-notifier">project on GitHub</a>.
				<p>©%d Rivian Gear Shop Notifier</p>
                <p class="disclaimer">Disclaimer: Rivian Gear Shop Notifier is not affiliated, sponsored, or associated with Rivian Automotive. Open Sourced, built and maintained by an active Rivian community member looking to build and provide ownership geared experiences for Rivian vehicle owners and fans of the business/product alike.</p>
			</div>
			<div class="referral">
				<p><a href="https://rivian.com/configurations/list?reprCode=%s">Interested in a Rivian? Use code "%s" to help support this and other Rivian focused projects.</a></p>
			</div>
		</div>
	</body>
	</html>
	`, productName, productLink, imagesHTML.String(), 2024, referralCode, referralCode)
}

// Handler function for AWS Lambda
func Handler(ctx context.Context, event events.DynamoDBEvent) (Response, error) {
	var products []Product

	for _, record := range event.Records {
		fmt.Printf("DynamoDB Event Received with EventName : %s , Change : %v", record.EventName, record.Change)

		// new product discovered
		if record.EventName == "INSERT" {
			id := record.Change.NewImage["Id"].String()
			name := record.Change.NewImage["Name"].String()
			price := record.Change.NewImage["Price"].String()
			url := record.Change.NewImage["GearShopUrl"].String()
			var productImages []string

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
			referralCode := os.Getenv("REFERRAL_CODE")

			emailBody := EmailTemplate(name, fmt.Sprintf("%s%s", GEAR_SHOP_HOSTNAME, url), productImages, 2024, referralCode)

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
						Html: &ses.Content{
							Data: aws.String(emailBody),
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
