package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/gocolly/colly/v2"
)

// ProductInfo represents the structure of the product information we want to extract.
type ProductInfo struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	SKU   string `json:"sku"`
	Price string `json:"price"`
	URL   string `json:"url"`
}

type Response struct {
	Message          string `json:"message"`
	NumberDiscovered int    `json:"numberDiscovered`
	NumberIndexed    int    `json:"numberIndexed`
}

var db *dynamodb.DynamoDB
var existingProducts map[string]bool

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
	db = dynamodb.New(aws_session)
}

// Load existing products from DynamoDB
func loadExistingProducts() error {
	input := &dynamodb.ScanInput{
		TableName: aws.String(os.Getenv("DYNAMODB_TABLE_NAME")),
	}

	result, err := db.Scan(input)
	if err != nil {
		return err
	}

	existingProducts = make(map[string]bool)
	for _, item := range result.Items {
		if attr, ok := item["Id"]; ok {
			existingProducts[*attr.S] = true // Store product.Id in the map
		}
	}
	return nil
}

func containsDollarSign(s string) bool {
	return strings.Contains(s, "$")
}

// extractPrice finds the price in the HTML element that contains a dollar sign.
func extractPrice(e *colly.HTMLElement) string {
	var price string
	priceFound := false
	// Iterate over all <p> elements to find the one containing a dollar sign
	e.ForEach("p", func(_ int, el *colly.HTMLElement) {
		if el.Text != "" && containsDollarSign(el.Text) {
			price = el.Text
			priceFound = true
		}
	})
	// If price is not found, set it to "N/A"
	if !priceFound {
		price = "N/A"
	}
	return price
}

// Extract SKU from the URL if it exists
func extractSku(e *colly.HTMLElement) string {
	var sku string
	if skuIndex := len(e.Request.URL.String()); skuIndex > 0 {
		// Check if the URL has a SKU parameter
		if e.Request.URL.Query().Get("sku") != "" {
			sku = e.Request.URL.Query().Get("sku")
		}
	} else {
		// If SKU is not present in the URL, leave it empty
		sku = "N/A"
	}
	return sku
}

// storeProduct stores a new product entry in DynamoDB
func storeProduct(productInfo ProductInfo) error {
	input := &dynamodb.PutItemInput{
		TableName: aws.String(os.Getenv("DYNAMODB_TABLE_NAME")), // Use environment variable for table name
		Item: map[string]*dynamodb.AttributeValue{
			"Id": {
				S: aws.String(productInfo.Id),
			},
			"Name": {
				S: aws.String(productInfo.Name),
			},
			"Price": {
				S: aws.String(productInfo.Price),
			},
			"GearShopUrl": {
				S: aws.String(productInfo.URL),
			},
			"DateIndexed": {
				S: aws.String(time.Now().Format(time.RFC3339)), // Current date in ISO 8601 format
			},
		},
	}

	_, err := db.PutItem(input)
	return err
}

// Handler function for AWS Lambda
func Handler(ctx context.Context) (Response, error) {
	// Load existing SKUs from DynamoDB
	if err := loadExistingProducts(); err != nil {
		log.Fatalf("Failed to load existing products: %v", err)
	}

	var allProducts []ProductInfo
	var discoveredProducts []ProductInfo
	c := colly.NewCollector()

	// On visiting the Gear Shop, extract product information
	c.OnHTML(`div[data-testid="store-grids-wrapper"] a[href]`, func(e *colly.HTMLElement) {
		url := e.Attr("href") // Get the product link

		// Extract Id from the url
		parts := strings.Split(url, "/")
		var id string
		if len(parts) > 0 {
			id = parts[len(parts)-1]
		}

		name := e.ChildText("p.rivian-css-1vv3rb5") // Get the product name

		// loaded dynamically - not available with colly framework (need headless browser)
		// price := e.ChildText("p.rivian-css-kxv3q2")
		price := extractPrice(e) // Get the product price
		sku := extractSku(e)     // Get the product price

		if id != "" && name != "" && url != "" && price != "" {
			productInfo := ProductInfo{Id: id, Name: name, SKU: sku, Price: price, URL: url}
			allProducts = append(allProducts, productInfo)

			// store product in dynamodb if not index already
			if existingProducts[productInfo.Id] {
				// log.Printf("Product %s found in existing products; not storing.", id)
				return // Skip given the item was already indexed
			} else {
				discoveredProducts = append(discoveredProducts, productInfo)
				if err := storeProduct(productInfo); err != nil {
					log.Printf("Failed to store product %s: %v", productInfo.Id, err)
				} else {
					log.Printf("Successfully indexed new product %s", productInfo.Id)
				}
			}
		}
	})

	// Visit the main gear shop page
	err := c.Visit("https://rivian.com/gear-shop")
	if err != nil {
		log.Printf("Error visiting Rivian Gear Shop: %v", err)
		return Response{}, err
	}

	// Convert the SKU information to JSON format
	skuJSON, err := json.Marshal(allProducts)
	if err != nil {
		log.Println("Error marshaling SKU data:", err)
		return Response{}, err
	}

	// You can add logging or additional processing here
	log.Printf("Found %d total product listings", len(allProducts))

	// Optionally, save the SKUs to a database or notify if there are new ones
	// (Implement that logic as per your requirement)

	if len(skuJSON) > 6*1024*1024 { // 6 MB limit
		log.Println("Response payload exceeds 6 MB")
		return Response{}, fmt.Errorf("response payload too large")
	}

	message := fmt.Sprintf("Indexed %d new product listings", len(discoveredProducts))
	return Response{Message: message, NumberDiscovered: len(allProducts), NumberIndexed: len(discoveredProducts)}, nil
}

func main() {
	// Check if the program is being run locally vs the lambda runtime
	if os.Getenv("ENVIRONMENT") == "local" || os.Getenv("AWS_SAM_LOCAL") != "" {
		// Run as a regular program for local testing
		log.Println("Running locally...")
		result, err := Handler(context.Background())
		if err != nil {
			log.Fatalf("Error: %v", err)
		}
		log.Println(result)
	} else {
		lambda.Start(Handler)
	}
}
