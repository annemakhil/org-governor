package main

import (
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/organizations"
	"log"
)

func createOrganizationalUnit(ou OrganizationalUnit) error {

	var ouParentId string
	orgC := makeOrgClient(profile, orgRole)

	// TODO: Add support for creating OU at any level
	rootExists := false
	Lro, err := orgC.ListRoots(&organizations.ListRootsInput{})
	if err != nil {
		return fmt.Errorf("ERROR: Failed to list roots in organization")
	}

	if ou.parent == "" {
		rootExists = true
		ouParentId = *Lro.Roots[0].Id
	} else {
		for _, root := range Lro.Roots {
			if ou.parent == *root.Name {
				ouParentId = *root.Id
				rootExists = true
			}
		}
	}

	if !rootExists {
		return fmt.Errorf("ERROR: Parent organizational unit %s does not exist.\n", ou.parent)
	}

	listOrganizationalUnitsResult, err := orgC.ListOrganizationalUnitsForParent(&organizations.ListOrganizationalUnitsForParentInput{
		ParentId: aws.String(ouParentId),
	})
	if err != nil {
		log.Printf("ERROR: Failed to list the organizational units under master with: %v", err)
	}

	for _, eOrganizationalUnit := range listOrganizationalUnitsResult.OrganizationalUnits {
		if ou.Name == *eOrganizationalUnit.Name {
			log.Printf("INFO: organizational unit %s already exists", ou.Name)
			return nil
		}
	}

	orgUnitInput := &organizations.CreateOrganizationalUnitInput{
		Name:     aws.String(ou.Name),
		ParentId: aws.String(ouParentId),
	}

	orgUnitOutput, err := orgC.CreateOrganizationalUnit(orgUnitInput)
	//TODO: Handle different errors in the response
	if err != nil {
		return fmt.Errorf("ERROR: Failed to create organizational unit %s with: %v", ou.Name, err)
	}
	ou.ID = *orgUnitOutput.OrganizationalUnit.Id
	log.Printf("INFO: organizational unit %s created successfully with ID %s.\n", ou.Name, ou.ID)
	updateOrgYaml(ou)
	return nil
}
