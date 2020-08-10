package main

import (
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	cfm "github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/organizations"
	"github.com/aws/aws-sdk-go/service/s3"
	cli "github.com/urfave/cli/v2"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"os"
	"reflect"
)

const (
	version                  = "2.0"
	defaultAccountAccessRole = "OrganizationAccountAccessRole"
	iamUserBillingAccess     = "ALLOW"
	orgFeatureSet            = "ALL"
	defaultRegion            = "us-west-2"
)

var (
	orgRole, profile string
)

// Organization ...
type Organization struct {
	OrganizationalUnits []OrganizationalUnit `yaml:"organizationalunits"`
}

// OrganizationalUnit ...
type OrganizationalUnit struct {
	ID       string    `yaml:"id"`
	Name     string    `yaml:"name"`
	parent   string    `yaml:"parent"`
	Accounts []Account `yaml:"accounts"`
}

// Account ...
type Account struct {
	ID           string `yaml:"id"`
	Alias        string `yaml:"alias"`
	Email        string `yaml:"email"`
	root         string
	TemplateFile string `yaml:"template"`
}

func readOrgYaml() Organization {
	var ou Organization
	content, err := ioutil.ReadFile("organization.yaml")
	if err != nil {
		log.Fatalf("ERROR: failed to read the organization source file: %v", err)
	}
	err = yaml.Unmarshal(content, &ou)
	if err != nil {
		log.Fatalf("ERROR: failed in unmarshalling organizations file: %v", err)
	}
	return ou
}

func updateOrgYaml(input interface{}) {
	org := readOrgYaml()
	if reflect.TypeOf(input).Name() == "OrganizationalUnit" {
		ou := input.(OrganizationalUnit)
		org.OrganizationalUnits = append(org.OrganizationalUnits, ou)
	} else if reflect.TypeOf(input).Name() == "Account" {
		acc := input.(Account)
		for i, u := range org.OrganizationalUnits {
			if u.Name == acc.root {
				org.OrganizationalUnits[i].Accounts = append(org.OrganizationalUnits[i].Accounts, acc)
				break
			}
		}
	}
	content, err := yaml.Marshal(org)
	if err != nil {
		log.Fatalf("ERROR: Failed to marshal the organizational unit data: %v", err)
	}
	err = ioutil.WriteFile("organization.yaml", content, 0644)
	if err != nil {
		log.Fatalf("ERROR: Failed to update the organizations file: %v", err)
	}
}

func makeAwsSession(profile string) *session.Session {
	sess := session.Must(session.NewSessionWithOptions(
		session.Options{
			Profile:           profile,
			SharedConfigState: session.SharedConfigEnable,
		},
	))
	return sess
}

func getCfmClient(profile, assumeRole string) *cfm.CloudFormation {
	var cfmC *cfm.CloudFormation
	sess := makeAwsSession(profile)
	if assumeRole != "" {
		cfmC = cfm.New(sess, &aws.Config{
			Credentials: stscreds.NewCredentials(sess, assumeRole),
			Region:      aws.String(defaultRegion),
		})
		return cfmC
	}
	cfmC = cfm.New(sess, aws.NewConfig().WithRegion(defaultRegion))
	return cfmC
}

func getS3Client(profile, assumeRole string) *s3.S3 {
	var s3C *s3.S3
	sess := makeAwsSession(profile)
	if assumeRole != "" {
		s3C = s3.New(sess, &aws.Config{
			Credentials: stscreds.NewCredentials(sess, assumeRole),
			Region:      aws.String(defaultRegion),
		})
		return s3C
	}
	s3C = s3.New(sess, aws.NewConfig().WithRegion(defaultRegion))
	return s3C
}

func makeOrgClient(profile, assumeRole string) *organizations.Organizations {
	var orgC *organizations.Organizations
	sess := makeAwsSession(profile)
	if assumeRole != "" {
		orgC = organizations.New(sess, &aws.Config{
			Credentials: stscreds.NewCredentials(sess, assumeRole),
		})
		return orgC
	}
	orgC = organizations.New(sess)
	return orgC
}

func uniq(input []string) []string {
	u := make([]string, 0, len(input))
	m := make(map[string]bool)

	for _, val := range input {
		if _, ok := m[val]; !ok {
			m[val] = true
			u = append(u, val)
		}
	}

	return u
}

// Creating Organization ...
func CreateOrganization(ctx *cli.Context) error {
	orgC := makeOrgClient(profile, orgRole)
	createOrgInput := &organizations.CreateOrganizationInput{
		FeatureSet: aws.String(orgFeatureSet),
	}
	OrgOutput, err := orgC.CreateOrganization(createOrgInput)
	if err != nil {
		log.Printf("ERROR: Failed to create organization with: %v", err)
		return err
	}
	log.Println("Successfully created the organization with the current account as master account")
	log.Printf("Unique identifier of the organization is %s", *OrgOutput.Organization.Id)
	return nil
}

func main() {

	runCreateOU := func(ctx *cli.Context) error {
		ouName := ctx.String("name")
		ouParent := ctx.String("parent")
		if ouName == "" {
			return fmt.Errorf("ERROR: Name of the organizational unit is required")
		}
		ou := OrganizationalUnit{Name: ouName, parent: ouParent}
		err := createOrganizationalUnit(ou)
		return err
	}

	runCreateAccount := func(ctx *cli.Context) error {
		accountAlias := ctx.String("name")
		accountEmail := ctx.String("email")
		accountUnit := ctx.String("ou")
		acc := Account{Alias: accountAlias, Email: accountEmail, root: accountUnit}
		err := CreateAccount(acc)
		return err
	}

	runUpdatePolicy := func(ctx *cli.Context) error {
		if ctx.IsSet("accounts") {
			err := UpdatePolicies(ctx.StringSlice("accounts"), ctx.IsSet("updateiam"))
			return err
		}
		var acc []string
		org := readOrgYaml()
		for _, ou := range org.OrganizationalUnits {
			for _, a := range ou.Accounts {
				acc = append(acc, a.Alias)
			}
		}
		err := UpdatePolicies(acc, ctx.IsSet("updateiam"))
		return err
	}

	app := &cli.App{
		Name:        "organization governor",
		Version:     version,
		Description: "A governor to manage organizations in aws",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "profile", Aliases: []string{"p"}, Value: "default", Destination: &profile},
			&cli.StringFlag{Name: "role", Usage: "Role to be assumed to interact with Organizations", Destination: &orgRole},
		},
		Commands: []*cli.Command{
			{
				Name:        "create-organization",
				Aliases:     []string{"co"},
				Usage:       "use it to create organization with an existing account",
				Description: "Make the existing account as organization root account",
				Action:      CreateOrganization,
			},
			{
				Name:        "create-ou",
				Aliases:     []string{"cr-ou"},
				Usage:       "use it to create organization unit",
				Description: "Create an organizational unit to segregate accounts based on the requirement",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Usage: "`name` for the organizational unit", Required: true},
					&cli.StringFlag{Name: "parent", Usage: "`parent` for the organizational unit"},
				},
				Action: runCreateOU,
			},
			{
				Name:        "create-account",
				Aliases:     []string{"cr-acc"},
				Usage:       "use it to create account unit",
				Description: "Create an account in the organization and move it to desired OU",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Usage: "`name` for the organizational unit"},
					&cli.StringFlag{Name: "email", Usage: "`parent` for the organizational unit", Required: true},
					&cli.StringFlag{Name: "ou", Usage: "Organizational Unit to move the account"},
				},
				Action: runCreateAccount,
			},
			{
				Name:        "update-policy",
				Aliases:     []string{"up-pol"},
				Usage:       "Use it to update the account policy",
				Description: "Update the respective accounts policy",
				Flags: []cli.Flag{
					&cli.StringSliceFlag{Name: "accounts", Aliases: []string{"acc"}, Usage: "Pass the accounts for which the policy to be updated"},
					&cli.BoolFlag{Name: "updateiam", Usage: "Flag to inform whether to update the iam groups or not"},
				},
				Action: runUpdatePolicy,
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(255)
	}
}
