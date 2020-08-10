package main

import (
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/organizations"
	"io/ioutil"
	"log"
	"strings"
	"time"
)

func CreateAccount(acc Account) error {
	var OrganizationUnitID, ParentID string
	orgC := makeOrgClient(profile, orgRole)
	if acc.root != "" {
		Lro, err := orgC.ListRoots(&organizations.ListRootsInput{})
		if err != nil {
			return fmt.Errorf("ERROR: Failed to list the roots in organization")
		}
		ParentID = *Lro.Roots[0].Id
		// Script is limited to check the OrganizationalUnits upto 1 level
		// TODO: Check for all the levels
		ListOrgUnitsInput := &organizations.ListOrganizationalUnitsForParentInput{
			ParentId: Lro.Roots[0].Id,
		}

		ListOrgUnitOutput, err := orgC.ListOrganizationalUnitsForParent(ListOrgUnitsInput)
		if err != nil {
			return fmt.Errorf("ERROR: Failed to list Organizational units under master")
		}
		ouExists := false
		for _, i := range ListOrgUnitOutput.OrganizationalUnits {
			if *i.Name == acc.root {
				OrganizationUnitID = *i.Id
				ouExists = true
				break
			}
		}
		if !ouExists {
			log.Printf("INFO: OrganizationUnit %s does not exist", acc.root)
			return nil
		}
	}
	accountsList, err := orgC.ListAccounts(&organizations.ListAccountsInput{})
	if err != nil {
		return fmt.Errorf("ERROR: Failed to list accounts: %v", err)
	}
	//TODO: Reduce the duplicacy with the NextToken
	for _, l := range accountsList.Accounts {
		if *l.Name == acc.Alias {
			return fmt.Errorf("ERROR: The account %s is already existed. Try using another name.", acc.Alias)
		}
	}
	nextT := accountsList.NextToken
	for nextT != nil {
		accountsList, err := orgC.ListAccounts(&organizations.ListAccountsInput{NextToken: nextT})
		if err != nil {
			return fmt.Errorf("ERROR: Failed to list accounts: %v", err)
		}
		for _, l := range accountsList.Accounts {
			if acc.Alias == *l.Name {
				return fmt.Errorf("ERROR: The account %s is already existed. Try using another name.", acc.Alias)
			}
		}
		nextT = accountsList.NextToken
	}

	accInput := &organizations.CreateAccountInput{
		AccountName:            aws.String(acc.Alias),
		Email:                  aws.String(acc.Email),
		IamUserAccessToBilling: aws.String(iamUserBillingAccess),
	}

	accOutput, err := orgC.CreateAccount(accInput)
	if err != nil {
		return fmt.Errorf("ERROR: Account %s creation failed with: %v", acc.Alias, err)
	}

	accCreateRequestId := accOutput.CreateAccountStatus.Id

	descCreateStatusInput := &organizations.DescribeCreateAccountStatusInput{
		CreateAccountRequestId: accCreateRequestId,
	}

	accCreated := false
	for i := 0; i < 15; i++ {
		descCreateStatusOutput, _ := orgC.DescribeCreateAccountStatus(descCreateStatusInput)
		if *descCreateStatusOutput.CreateAccountStatus.State == "IN_PROGRESS" {
			time.Sleep(60 * time.Second)
			continue
		} else if *descCreateStatusOutput.CreateAccountStatus.State == "SUCCEEDED" {
			accCreated = true
			acc.ID = *descCreateStatusOutput.CreateAccountStatus.AccountId
			log.Printf("INFO: Account is created succesfully with ID: %s \n", acc.ID)
			break
		} else {
			return fmt.Errorf("Account %s creation failed with %s", acc.Alias, *descCreateStatusOutput.CreateAccountStatus.FailureReason)
		}
	}
	if !accCreated {
		return fmt.Errorf("Account Creation took too long to complete. Please have a manual check of the status")
	}

	log.Printf("INFO: Moving account %s from root to %s", acc.Alias, acc.root)
	if acc.root != "" {
		moveAccountInput := &organizations.MoveAccountInput{
			AccountId:           aws.String(acc.ID),
			DestinationParentId: aws.String(OrganizationUnitID),
			SourceParentId:      aws.String(ParentID),
		}

		// TODO: Handle the case when account creation is succeeded but moving is failed
		_, err := orgC.MoveAccount(moveAccountInput)
		if err != nil {
			return fmt.Errorf("ERROR: Failed to move the account %s to the destination organizational unit %s", acc.Alias, acc.root)
		}
	}
	content, err := ioutil.ReadFile("policies/template_policy.json")
	if err != nil {
		fmt.Errorf("ERROR: Failed to read the policy template with: %v", err)
	}
	dstFile := "policies/" + strings.Title(acc.Alias) + "-Policies"
	err = ioutil.WriteFile(dstFile, content, 0644)
	if err != nil {
		fmt.Errorf("ERROR: Failed to create new policy template file with: %v", err)
	}
	acc.TemplateFile = dstFile
	updateOrgYaml(acc)
	err = UpdatePolicies([]string{acc.Alias}, true)
	return err

}
