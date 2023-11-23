package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/organizations"
)

type Action string

const (
	ApplySCP   Action = "apply_scp"
	DeleteRole Action = "delete_role"
	// Default region to be used if the region is not specified by the user
	DefaultRegion = "us-east-1"
)

type Request struct {
	Action                 Action `json:"action"`
	TargetAccountID        string `json:"target_account_id"`
	RoleToAssume           string `json:"role_to_assume"`
	TargetRoleName         string `json:"target_role_name,omitempty"`       // Used for delete_role action
	OrgManagementAccountID string `json:"org_management_account,omitempty"` // Used for apply_scp action
	Region                 string `json:"region,omitempty"`
}

func HandleRequest(ctx context.Context, request Request) (string, error) {
	if request.TargetAccountID == "" || request.RoleToAssume == "" {
		return "", errors.New("targetAccountID and roleToAssume are required")
	}

	// Default to us-east-1 if Region is not provided
	if request.Region == "" {
		request.Region = DefaultRegion
	}

	sess := session.Must(session.NewSession(&aws.Config{
		Region: aws.String(request.Region),
	}))

	switch request.Action {
	case ApplySCP:
		if request.OrgManagementAccountID == "" {
			return "", errors.New("managementAccount is required for apply_scp action")
		}
		return applySCP(ctx, sess, request.OrgManagementAccountID, request.TargetAccountID, request.RoleToAssume)
	case DeleteRole:
		if request.TargetRoleName == "" {
			return "", errors.New("targetRoleName is required for delete_role action")
		}
		return deleteRole(ctx, sess, request.TargetAccountID, request.RoleToAssume, request.TargetRoleName)
	default:
		return "", errors.New("invalid action")
	}
}

func applySCP(ctx context.Context, sess *session.Session, managementAccount, targetAccountID, roleToAssume string) (string, error) {
	creds := stscreds.NewCredentials(sess, fmt.Sprintf("arn:aws:iam::%s:role/%s", managementAccount, roleToAssume))
	svc := organizations.New(sess, &aws.Config{Credentials: creds})

	// Define the SCP policy
	scpPolicy := `{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Effect": "Deny",
				"NotAction": [
					"cloudwatch:*",
					"cloudtrail:*",
					"guardduty:*"
				],
				"Resource": "*"
			}
		]
	}`

	// Create the SCP
	createPolicyInput := &organizations.CreatePolicyInput{
		Content:     aws.String(scpPolicy),
		Description: aws.String("Highly Restrictive SCP"),
		Name:        aws.String("HighlyRestrictiveSCP"),
		Type:        aws.String("SERVICE_CONTROL_POLICY"),
	}

	policyResp, err := svc.CreatePolicy(createPolicyInput)
	if err != nil {
		return "", fmt.Errorf("error creating SCP: %v", err)
	}

	// Attach the SCP
	attachPolicyInput := &organizations.AttachPolicyInput{
		PolicyId: policyResp.Policy.PolicySummary.Id,
		TargetId: aws.String(targetAccountID),
	}

	_, err = svc.AttachPolicy(attachPolicyInput)
	if err != nil {
		return "", fmt.Errorf("error attaching SCP to account %s: %v", targetAccountID, err)
	}

	return fmt.Sprintf("SCP applied to account %s with policy ID %s", targetAccountID, *policyResp.Policy.PolicySummary.Id), nil
}

func deleteRole(ctx context.Context, sess *session.Session, targetAccountID, roleToAssume, targetRoleName string) (string, error) {
	creds := stscreds.NewCredentials(sess, fmt.Sprintf("arn:aws:iam::%s:role/%s", targetAccountID, roleToAssume))
	svc := iam.New(sess, &aws.Config{Credentials: creds})

	// List attached managed policies
	listPoliciesOutput, err := svc.ListAttachedRolePolicies(&iam.ListAttachedRolePoliciesInput{RoleName: aws.String(targetRoleName)})
	if err != nil {
		return "", fmt.Errorf("error listing attached policies for role %s in account %s: %v", targetRoleName, targetAccountID, err)
	}

	// Detach each managed policy
	for _, policy := range listPoliciesOutput.AttachedPolicies {
		_, err = svc.DetachRolePolicy(&iam.DetachRolePolicyInput{
			RoleName:  aws.String(targetRoleName),
			PolicyArn: policy.PolicyArn,
		})
		if err != nil {
			return "", fmt.Errorf("error detaching policy %s from role %s in account %s: %v", *policy.PolicyArn, targetRoleName, targetAccountID, err)
		}
	}

	// List inline policies
	listInlinePoliciesOutput, err := svc.ListRolePolicies(&iam.ListRolePoliciesInput{RoleName: aws.String(targetRoleName)})
	if err != nil {
		return "", fmt.Errorf("error listing inline policies for role %s in account %s: %v", targetRoleName, targetAccountID, err)
	}

	// Delete each inline policy
	for _, policyName := range listInlinePoliciesOutput.PolicyNames {
		_, err = svc.DeleteRolePolicy(&iam.DeleteRolePolicyInput{
			RoleName:   aws.String(targetRoleName),
			PolicyName: policyName,
		})
		if err != nil {
			return "", fmt.Errorf("error deleting inline policy %s from role %s in account %s: %v", *policyName, targetRoleName, targetAccountID, err)
		}
	}

	// Delete the role
	_, err = svc.DeleteRole(&iam.DeleteRoleInput{RoleName: aws.String(targetRoleName)})
	if err != nil {
		return "", fmt.Errorf("error deleting role %s in account %s: %v", targetRoleName, targetAccountID, err)
	}

	return fmt.Sprintf("Role %s and its policies are detached and deleted in account %s", targetRoleName, targetAccountID), nil
}

func main() {
	lambda.Start(HandleRequest)
}
