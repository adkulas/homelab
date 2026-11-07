#!/usr/bin/env python3
"""Prove disabled scope import, then supervised clients on the pilot bridge only."""
import cluster_check
import dhcp_check

if __name__ == '__main__':
    cluster_check.main()
    dhcp_check.main(staged=True)
