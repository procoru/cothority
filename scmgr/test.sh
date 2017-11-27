#!/usr/bin/env bash

DBG_TEST=2
# Debug-level for app
DBG_APP=2
#DBG_SRV=3

. $GOPATH/src/gopkg.in/dedis/onet.v1/app/libtest.sh

main(){
	startTest
	buildConode github.com/dedis/cothority/skipchain
	CFG=$BUILDDIR/config.bin
	# test Restart
	# test Config
	# test Create
	# test Join
	# test Add
	# test Index
	# test Html
	# test Fetch
	# test AuthLink
	test Friend
	stopTest
}

testFriend(){
	startCl
	runCoBG 3
	cat co1/public.toml > group1.toml
	cat co[12]/public.toml > group12.toml
	cat co[123]/public.toml > group123.toml
	hosts=()
	for h in 1 2 3; do
		host[$h]="localhost:$(( 2000 + 2 * h ))"
		runSc admin link -priv co$h/private.toml
		runSc auth ${host[$h]} 1
	done
	setupGenesis group1.toml
	testFail runSc skipchain add $ID group12.toml
	testOK runSc admin follow -trust 0 ${host[2]} $ID
	testOK runSc skipchain add $ID group12.toml

	setupGenesis group1.toml
	testFail runSc skipchain add $ID group12.toml
	testFail runSc admin follow -search ${host[3]} $ID
	testOK runSc admin follow -search ${host[2]} $ID
	testOK runSc skipchain add $ID group12.toml
	testFail runSc skipchain add $ID group123.toml
	testFail runSc admin follow -lookup ${host[3]} ${host[2]} $ID
	testOK runSc admin follow -lookup ${host[2]} ${host[3]} $ID
	testOK runSc skipchain add $ID group123.toml
}

testAuthLink(){
	startCl
	setupGenesis
	testOK [ -n "$ID" ]
	ID=""
	testFail runSc admin auth localhost:2002 1
	testFail [ -n "$ID" ]
	testOK runSc admin link -priv co1/private.toml
	testOK runSc admin link -priv co2/private.toml
	testOK runSc admin auth localhost:2004 1
	testOK runSc admin auth localhost:2002 1
	setupGenesis
	testOK [ -n "$ID" ]
}

testFetch(){
	startCl
	setupGenesis
	rm -f $CFG
	testFail runSc list fetch
	testOK runSc list fetch public.toml
	testGrep 2002 runSc list known
	testGrep 2004 runSc list known
}

testHtml(){
	startCl
	testOK runSc sc create -html http://dedis.ch public.toml
	ID=$( runSc list known | head -n 1 | sed -e "s/.*block \(.*\) with.*/\1/" )
	html=$(mktemp)
	echo "TestWeb" > $html
	echo $ID - $html
	testOK runSc sc addWeb $ID $html
	rm -f $html
}

testRestart(){
	startCl
	setupGenesis
	pkill -9 conode 2> /dev/null
	runCoBG 1 2
	testOK runSc sc add $ID public.toml
}

testAdd(){
	startCl
	setupGenesis
	testFail runSc sc add 1234 public.toml
	testOK runSc sc add $ID public.toml
	runCoBG 3
	runGrepSed "Latest block of" "s/.* //" runSc sc update $ID
	LATEST=$SED
	testOK runSc sc add $LATEST public.toml
}

setupGenesis(){
	runGrepSed "Created new" "s/.* //" runSc sc create ${1:-public.toml}
	ID=$SED
}

testJoin(){
	startCl
	runGrepSed "Created new" "s/.* //" runSc sc create public.toml
	ID=$SED
	rm -f $CFG
	testGrep "Didn't find any" runSc list known
	testFail runSc list join public.toml 1234
	testGrep "Didn't find any" runSc list known
	testOK runSc list join public.toml $ID
	testGrep $ID runSc list known -l
}

testCreate(){
	startCl
	testGrep "Didn't find any" runSc list known -l
	testFail runSc sc create
	testOK runSc sc create public.toml
	testGrep "Genesis-block" runSc list known -l
}

testIndex(){
	startCl
	setupGenesis
	touch random.html

	testFail runSc list index
	testOK runSc list index $PWD
	testGrep "$ID" cat index.html
	testGrep "127.0.0.1" cat index.html
	testGrep "$ID" cat "$ID.html"
	testGrep "127.0.0.1" cat "$ID.html"
	testNFile random.html
}

testConfig(){
	startCl
	OLDCFG=$CFG
	CFGDIR=$( mktemp -d )
	CFG=$CFGDIR/config.bin
	rmdir $CFGDIR
	head -n 4 public.toml > one.toml
	testOK runSc sc create one.toml
	testOK runSc sc create public.toml
	rm -rf $CFGDIR
	CFG=$OLDCFG
}

runSc(){
	dbgRun ./$APP -c $CFG -d $DBG_APP $@
}

startCl(){
	rm -f $CFG
	runCoBG 1 2
}

main
