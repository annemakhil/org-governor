package main

import (
	"bytes"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	cfm "github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/google/uuid"
	"io/ioutil"
	"log"
	"strings"
)

const (
	templateBucket = "<yet to be decided>"
)

func UpdatePolicies(acc []string, gu bool) error {
	var org Organization
	org = readOrgYaml()
	var IamAccId, ProdAccId string
	for _, ou := range org.OrganizationalUnits {
		for _, acc := range ou.Accounts {
			if acc.Alias == "aqfer-iam" {
				IamAccId = acc.ID
			} else if acc.Alias == "aqfer-prod" {
				ProdAccId = acc.ID
			}
		}
	}
	iamInput := make(map[string][]string)
	for _, l := range acc {
		for _, ou := range org.OrganizationalUnits {
			for _, a := range ou.Accounts {
				if l == a.Alias {
					log.Printf("Updating Policy template for %s.\n", a.Alias)
					orgAccAccessRole := fmt.Sprintf("arn:aws:iam::%s:role/OrganizationAccountAccessRole", a.ID)
					rand, _ := uuid.NewRandom()
					changeSetName := aws.String(fmt.Sprintf("cs-%s", rand.String()))
					// templateBucket := "akhil-org-test"
					templateKey := strings.Title(a.Alias) + "-Policies"
					s3C := getS3Client(profile, orgRole)
					content, err := ioutil.ReadFile(a.TemplateFile)
					if err != nil {
						return fmt.Errorf("ERROR: Failed to read the policy file %s with: %v", a.TemplateFile, err)
					}
					_, err = s3C.PutObject(&s3.PutObjectInput{
						Bucket: aws.String(templateBucket),
						Body:   bytes.NewReader(content),
						Key:    aws.String(templateKey),
					})
					if err != nil {
						return fmt.Errorf("ERROR: Failed to upload the template file to s3 for %s with: %v", a.Alias, err)
					}
					grantee := "emailAddress=" + a.Email
					_, err = s3C.PutObjectAcl(&s3.PutObjectAclInput{
						Bucket:    aws.String(templateBucket),
						Key:       aws.String(templateKey),
						GrantRead: aws.String(grantee),
					})
					if err != nil {
						return fmt.Errorf("ERROR: Failed to apply ACL to the uploaded template file with: %v", err)
					}
					s3URL := fmt.Sprintf("https://%s.s3-%s.amazonaws.com/%s", templateBucket, defaultRegion, templateKey)
					cfmC := getCfmClient(profile, orgAccAccessRole)
					dso, err := cfmC.DescribeStacks(&cfm.DescribeStacksInput{StackName: aws.String(strings.Title(a.Alias) + "-Policies")})
					createInput := cfm.CreateChangeSetInput{
						ChangeSetName: changeSetName,
						StackName:     aws.String(strings.Title(a.Alias) + "-Policies"),
						TemplateURL:   aws.String(s3URL),
						Capabilities:  aws.StringSlice([]string{"CAPABILITY_NAMED_IAM"}),
					}
					if err != nil {
						if strings.Contains(err.Error(), "does not exist") {
							createInput.SetChangeSetType("CREATE")
							createInput.SetParameters([]*cfm.Parameter{
								&cfm.Parameter{ParameterKey: aws.String("IamAccountID"), ParameterValue: aws.String(IamAccId)},
								&cfm.Parameter{ParameterKey: aws.String("ProdccountID"), ParameterValue: aws.String(ProdAccId)},
							})
						} else {
							return fmt.Errorf("ERROR: Failed to retrieve stack status: %v", err.Error())
						}
					} else {
						status := *dso.Stacks[0].StackStatus
						if strings.Contains(status, "COMPLETE") && !strings.Contains(status, "PROGRESS") {
							createInput.SetChangeSetType("UPDATE")
							createInput.SetParameters([]*cfm.Parameter{
								&cfm.Parameter{ParameterKey: aws.String("IamAccountID"), ParameterValue: aws.String(IamAccId)},
								&cfm.Parameter{ParameterKey: aws.String("ProdccountID"), ParameterValue: aws.String(ProdAccId)},
							})
						} else {
							return fmt.Errorf("ERROR: Stack is busy with status: %s", status)
						}
					}
					result, err := cfmC.CreateChangeSet(&createInput)
					if err != nil {
						return fmt.Errorf("ERROR: Failed to create change set: %v", err.Error())
					}
					log.Println("INFO: waiting for change set creation to complete...")
					err = cfmC.WaitUntilChangeSetCreateComplete(&cfm.DescribeChangeSetInput{ChangeSetName: result.Id})
					dcso, derr := cfmC.DescribeChangeSet(&cfm.DescribeChangeSetInput{ChangeSetName: result.Id})
					if derr != nil {
						return fmt.Errorf("ERROR: Failed to describe change set: %v", err.Error())
					}
					if err != nil {
						reason := *dcso.StatusReason
						if *dcso.Status == "FAILED" {
							if strings.Contains(reason, "No updates") ||
								strings.Contains(reason, "didn't contain changes") {
								log.Println("INFO: No changes detected")
								return nil
							}
							return fmt.Errorf("change set creation failed: %s", reason)
						}
					}
					log.Println("INFO: executing change set")
					_, err = cfmC.ExecuteChangeSet(&cfm.ExecuteChangeSetInput{ChangeSetName: result.Id})
					if err != nil {
						return fmt.Errorf("ERROR: Failed to execute change set: %v", err.Error())
					}
					if *createInput.ChangeSetType == "CREATE" {
						err = cfmC.WaitUntilStackCreateComplete(&cfm.DescribeStacksInput{StackName: aws.String(strings.Title(a.Alias) + "-Policies")})
						if err != nil {
							return fmt.Errorf("ERROR: Failed to create stack: %v", err.Error())
						}
						log.Println("INFO: Stack is created successfully")
					} else {
						err = cfmC.WaitUntilStackUpdateComplete(&cfm.DescribeStacksInput{StackName: aws.String(strings.Title(a.Alias) + "-Policies")})
						if err != nil {
							return fmt.Errorf("ERROR: Failed to update stack: %v", err.Error())
						}
						log.Println("INFO: Stack is updated successfully")
					}
					dso, err = cfmC.DescribeStacks(&cfm.DescribeStacksInput{StackName: aws.String(strings.Title(a.Alias) + "-Policies")})
					for _, so := range dso.Stacks[0].Outputs {
						iamInput[*so.ExportName] = append(iamInput[*so.ExportName], *so.OutputValue)
					}
				}
			}
		}
	}
	if gu {
		AddToGroups(iamInput)
	}
	return nil
}

func AddToGroups(input map[string][]string) error {
	log.Println("INFO: Updating iam groups")
	var org Organization
	var a Account
	org = readOrgYaml()
	for _, ou := range org.OrganizationalUnits {
		for _, acc := range ou.Accounts {
			if acc.Alias == "aqfer-iam" {
				a = acc
			}
		}
	}
	orgAccAccessRole := fmt.Sprintf("arn:aws:iam::%s:role/OrganizationAccountAccessRole", a.ID)
	var params []*cfm.Parameter
	cfmC := getCfmClient(profile, orgAccAccessRole)
	dso, _ := cfmC.DescribeStacks(&cfm.DescribeStacksInput{StackName: aws.String(strings.Title(a.Alias) + "-Policies")})
	for g, r := range input {
		for _, p := range dso.Stacks[0].Parameters {
			if g == *p.ParameterKey {
				iamParams := strings.Split(*p.ParameterValue, ",")
				iamParams = append(iamParams, r...)
				params = append(params, &cfm.Parameter{ParameterKey: p.ParameterKey, ParameterValue: aws.String(strings.Join(uniq(iamParams), ","))})
			}
		}
	}
	// templateBucket := "akhil-org-test"
	templateKey := strings.Title(a.Alias) + "-Policies"
	s3C := getS3Client(profile, orgRole)
	content, err := ioutil.ReadFile(a.TemplateFile)
	if err != nil {
		return fmt.Errorf("ERROR: Failed to read the policy file %s", a.TemplateFile)
	}
	_, err = s3C.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(templateBucket),
		Body:   bytes.NewReader(content),
		Key:    aws.String(templateKey),
	})
	if err != nil {
		return fmt.Errorf("ERROR: Failed to upload template file: %v \n", err)
	}
	grantee := "emailAddress=" + a.Email
	_, err = s3C.PutObjectAcl(&s3.PutObjectAclInput{
		Bucket:    aws.String(templateBucket),
		Key:       aws.String(templateKey),
		GrantRead: aws.String(grantee),
	})
	if err != nil {
		return fmt.Errorf("ERROR: Failed to apply ACL to the uploaded template file with: %v", err)
	}
	s3URL := fmt.Sprintf("https://%s.s3-%s.amazonaws.com/%s", templateBucket, defaultRegion, templateKey)
	stackInput := &cfm.UpdateStackInput{
		Capabilities: aws.StringSlice([]string{"CAPABILITY_NAMED_IAM"}),
		StackName:    aws.String(strings.Title(a.Alias) + "-Policies"),
		TemplateURL:  aws.String(s3URL),
		Parameters:   params,
	}
	log.Println("INFO: Updating the iam-groups stack")
	_, err = cfmC.UpdateStack(stackInput)
	if err != nil {
		return fmt.Errorf("ERROR: Stack %s update failed with status: %v", strings.Title(a.Alias), err)
	}
	err = cfmC.WaitUntilStackUpdateComplete(&cfm.DescribeStacksInput{StackName: aws.String(templateKey)})
	if err != nil {
		return fmt.Errorf("ERROR: Failed to update the iam-groups stack with: %v", err)
	}
	log.Println("INFO: Successfully updated the iam-groups stack")
	return nil
}
