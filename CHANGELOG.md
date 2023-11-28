# Changelog

## [1.1] - 2023-11-27

* Added `detach_policies` action, which leaves the IAM role in place but detaches IAM policies and deletes inline policies from the role.
* Service control policy (SCP) for the `apply_scp` action is now defined as `scpPolicy` in `switch.conf`

## [1.0.0] - 2023-11-22

Initial release