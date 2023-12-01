# Changelog

## [1.2.0] - 2023-11-30

* Added `revoke_sessions` action, which creates and attaches a session revocation policy to your choice of a named role or every customer managed role in the target account.
* Improved SCP in `switch.conf` to be less restrictive and reduce the likelihood of it breaking an application while maintaining the account's state.

## [1.1.0] - 2023-11-27

* Added `detach_policies` action, which leaves the IAM role in place but detaches IAM policies and deletes inline policies from the role.
* Service control policy (SCP) for the `apply_scp` action is now defined as `scpPolicy` in `switch.conf`

## [1.0.0] - 2023-11-22

Initial release