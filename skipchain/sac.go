package skipchain

/*
Skipchain Access Control - tells the node which skipchains he should accept and which ones
he should reject.

# Creation of Skipchains and Adding new Blocks
Whenever the first block of a skipchain is created, the leader collects all data, creates the roster with himself as
the first entry, and sends this block as the new genesis-block to all nodes of the roster. At the creation of a new
skipchain, no collective signature is made, which means that the leader can add random conodes to the genesis skipblock,
even blocks that do not want to be part of the skipchain.

When a new block is created, it can change the roster to either indicate a change in the leader or to add or remove
nodes. But only the cothority defined by the roster of the latest valid block is asked to cosign the new block.

This means that each conode only has the possibility to interact whenever a new block is created.

# Actions and Elements
Each conode has a list of actions to go through whenever it needs to validate a new block. First all refusing
actions are checked, then all accepting actions.

## Elements
Each action is defined on one of three different kind of elements: skipchain ids, skipchains or conodes.
Skipchain id – only the stored id is verified against the new block
Skipchain – whenever a match needs to be made, the latest block is fetched and all conodes of the latest roster are used for the match
Single conode - only this conode is checked
Actions
For each element, two refuse and two accept actions are possible, both differing in the depth of the comparison:
Refuse any: if any of the conodes of the new block matches any of the conodes in the element
Accept all: if all of the conodes of the new block are present in one or more 	elements marked accept all
(Refuse|accept) leader: if the leader of the new block matches any of the conodes in the element
First all refuse actions are verified. If none match, all accept actions are verified. If still none match, the default action is taken:
Default Action
Different kind of default actions are possible in the case that none of the refuse or accept actions match:
Refuse all – this is the default and gives the greatest security
Follow known 	– accept evolution of all skipchains where the conode accepted at 	least one block – medium security, but closest to the original spirit of skipchains
Accept all – lowest security – only use this in a private network

*/
