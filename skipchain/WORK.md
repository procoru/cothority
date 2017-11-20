Work in progress:
- finishing api_tests
  - SettingAuthentication
  - (Add|Delete|List)Link
- current problem:
  - when giving a skipchain-id to AddLink, how to retrieve the block?
    - current solution: service looks if it can find the block
      in any existing neighbour. If found, stores it as a skipblock,
      else it stores its genesis-id only.
- TODO:
  - if a FollowIDs is found, convert it to a Follow.
  - eventually moving AuthSkipchain to the service
