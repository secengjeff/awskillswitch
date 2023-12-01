package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/organizations"
)

type Action string

const (
	ApplySCP       Action = "apply_scp"
	DeleteRole     Action = "delete_role"
	DetachPolicies Action = "detach_policies"
	RevokeSessions Action = "revoke_sessions"
	// Default region to be used if the region is not specified by the user
	DefaultRegion = "us-east-1"
)

type Request struct {
	Action                 Action `json:"action"`
	TargetAccountID        string `json:"target_account_id"`
	RoleToAssume           string `json:"role_to_assume"`
	TargetRoleName         string `json:"target_role_name,omitempty"`       // Used for actions other than apply_scp
	OrgManagementAccountID string `json:"org_management_account,omitempty"` // Used for apply_scp action
	Region                 string `json:"region,omitempty"`
}

type Config struct {
	SwitchConfigVersion string `json:"switchConfigVersion"`
	SwitchPolicies      struct {
		SCPolicy json.RawMessage `json:"scpPolicy"`
	} `json:"switchPolicies"`
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
		// Load SCP from .conf file
		configFile := "switch.conf"
		config, err := loadConfig(configFile)
		if err != nil {
			return "", fmt.Errorf("error loading config file: %v", err)
		}
		return applySCP(ctx, sess, request.OrgManagementAccountID, request.TargetAccountID, request.RoleToAssume, config)
	case DetachPolicies, DeleteRole:
		if request.TargetRoleName == "" {
			return "", errors.New("targetRoleName is required for this action")
		}
		return manageRole(ctx, sess, request.Action, request.TargetAccountID, request.RoleToAssume, request.TargetRoleName)
	case RevokeSessions:
		if request.TargetRoleName == "" {
			return "", errors.New("targetRoleName is required for this action")
		}
		return revokeSession(ctx, sess, request.TargetAccountID, request.RoleToAssume, request.TargetRoleName)
	default:
		return "", errors.New("invalid action")
	}
}

// Load awskillswitch.conf if needed
func loadConfig(filename string) (*Config, error) {
	var config Config
	configFile, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(configFile, &config)
	if err != nil {
		return nil, err
	}
	return &config, nil
}

func revokeSession(ctx context.Context, sess *session.Session, targetAccountID, roleToAssume, targetRoleName string) (string, error) {
	// Assume role
	creds := stscreds.NewCredentials(sess, fmt.Sprintf("arn:aws:iam::%s:role/%s", targetAccountID, roleToAssume))
	svc := iam.New(sess, &aws.Config{Credentials: creds})

	// Give the invalidation policy a unique name based on the current time
	policyName := fmt.Sprintf("TokenInvalidationPolicy-%s", time.Now().Format("20060102-150405"))

	// Create the invalidation policy
	policyDocument := fmt.Sprintf(`{
        "Version": "2012-10-17",
        "Statement": [{
            "Effect": "Deny",
            "Action": "*",
            "Resource": "*",
            "Condition": {"DateLessThan": {"aws:TokenIssueTime": "%s"}}
        }]
    }`, time.Now().Format(time.RFC3339))

	createPolicyOutput, err := svc.CreatePolicy(&iam.CreatePolicyInput{
		PolicyName:     aws.String(policyName),
		PolicyDocument: aws.String(policyDocument),
		Description:    aws.String("Policy to invalidate all tokens at time of creation"),
	})
	if err != nil {
		return "", fmt.Errorf("error creating new policy: %v", err)
	}

	if targetRoleName == "ALL" {
		// Attach the policy to all roles
		return revokeSessionAllRoles(ctx, svc, createPolicyOutput.Policy.Arn, roleToAssume)
	} else {
		// Attach the policy to a specific role
		_, err = svc.AttachRolePolicy(&iam.AttachRolePolicyInput{
			RoleName:  aws.String(targetRoleName),
			PolicyArn: createPolicyOutput.Policy.Arn,
		})
		if err != nil {
			return "", fmt.Errorf("error attaching new policy to role %s in account %s: %v", targetRoleName, targetAccountID, err)
		}
		return fmt.Sprintf("New token recovation policy attached to role %s in account %s", targetRoleName, targetAccountID), nil
	}
}

func revokeSessionAllRoles(ctx context.Context, svc *iam.IAM, policyArn *string, assumedRoleName string) (string, error) {
	// List all roles
	input := &iam.ListRolesInput{}
	var result strings.Builder

	err := svc.ListRolesPages(input, func(page *iam.ListRolesOutput, lastPage bool) bool {
		for _, role := range page.Roles {
			// Skip the role assumed by the Lambda function
			if *role.RoleName == assumedRoleName {
				continue
			}

			// Attach the policy to each role
			_, err := svc.AttachRolePolicy(&iam.AttachRolePolicyInput{
				RoleName:  role.RoleName,
				PolicyArn: policyArn,
			})
			if err != nil {
				fmt.Printf("Error attaching policy to role %s: %v\n", *role.RoleName, err)
				continue
			}
			result.WriteString(fmt.Sprintf("Policy attached to role %s\n", *role.RoleName))
		}
		return !lastPage
	})

	if err != nil {
		return "", fmt.Errorf("error attaching policy to roles: %v", err)
	}

	return result.String(), nil
}

func applySCP(ctx context.Context, sess *session.Session, managementAccount, targetAccountID, roleToAssume string, config *Config) (string, error) {
	creds := stscreds.NewCredentials(sess, fmt.Sprintf("arn:aws:iam::%s:role/%s", managementAccount, roleToAssume))
	svc := organizations.New(sess, &aws.Config{Credentials: creds})

	// Convert byte slice to string
	scpPolicy := string(config.SwitchPolicies.SCPolicy)

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

// Actions involving role manipulation or deletion
func manageRole(ctx context.Context, sess *session.Session, action Action, targetAccountID, roleToAssume, targetRoleName string) (string, error) {
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

	// Delete the role if Action is delete_role
	if action == DeleteRole {
		_, err = svc.DeleteRole(&iam.DeleteRoleInput{RoleName: aws.String(targetRoleName)})
		if err != nil {
			return "", fmt.Errorf("error deleting role %s in account %s: %v", targetRoleName, targetAccountID, err)
		}
		return fmt.Sprintf("Role %s and its policies are detached and deleted in account %s", targetRoleName, targetAccountID), nil
	}
	return fmt.Sprintf("Policies detached from role %s in account %s", targetRoleName, targetAccountID), nil
}

func main() {
	lambda.Start(HandleRequest)
}
