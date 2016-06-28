// Copyright 2016 Mender Software AS
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//        http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.
package main

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_getImageId_noImageIdInFile_returnsEmptyId(t *testing.T) {
	mender := newDefaultTestMender()

	manifestFile, _ := os.Create("manifest")
	defer os.Remove("manifest")

	fileContent := "dummy_data"
	manifestFile.WriteString(fileContent)
	// rewind to the beginning of file
	//manifestFile.Seek(0, 0)

	mender.manifestFile = "manifest"

	assert.Equal(t, "", mender.GetCurrentImageID())
}

func Test_getImageId_malformedImageIdLine_returnsEmptyId(t *testing.T) {
	mender := newDefaultTestMender()

	manifestFile, _ := os.Create("manifest")
	defer os.Remove("manifest")

	fileContent := "IMAGE_ID"
	manifestFile.WriteString(fileContent)
	// rewind to the beginning of file
	//manifestFile.Seek(0, 0)

	mender.manifestFile = "manifest"

	assert.Equal(t, "", mender.GetCurrentImageID())
}

func Test_getImageId_haveImageId_returnsId(t *testing.T) {
	mender := newDefaultTestMender()

	manifestFile, _ := os.Create("manifest")
	defer os.Remove("manifest")

	fileContent := "IMAGE_ID=mender-image"
	manifestFile.WriteString(fileContent)
	mender.manifestFile = "manifest"

	assert.Equal(t, "mender-image", mender.GetCurrentImageID())
}

func newTestMender(runner *testOSCalls, config menderConfig, pieces MenderPieces) *mender {
	// fill out missing pieces

	if pieces.store == nil {
		pieces.store = NewMemStore()
	}

	if pieces.env == nil {
		if runner == nil {
			testrunner := newTestOSCalls("", -1)
			runner = &testrunner
		}
		pieces.env = &uBootEnv{runner}
	}

	if pieces.updater == nil {
		pieces.updater = &fakeUpdater{}
	}

	if pieces.device == nil {
		pieces.device = &fakeDevice{}
	}

	if pieces.authMgr == nil {
		if config.DeviceKey == "" {
			config.DeviceKey = "devkey"
		}

		cmdr := newTestOSCalls("mac=foobar", 0)
		pieces.authMgr = NewAuthManager(pieces.store, config.DeviceKey,
			&IdentityDataRunner{
				cmdr: &cmdr,
			})
	}

	if pieces.authReq == nil {
		pieces.authReq = &fakeAuthorizer{}
	}

	mender := NewMender(config, pieces)
	return mender
}

func newDefaultTestMender() *mender {
	return newTestMender(nil, menderConfig{}, MenderPieces{})
}

func Test_ForceBootstrap(t *testing.T) {
	// generate valid keys
	ms := NewMemStore()
	mender := newTestMender(nil,
		menderConfig{
			DeviceKey: "temp.key",
		},
		MenderPieces{
			store: ms,
		},
	)

	merr := mender.Bootstrap()
	assert.NoError(t, merr)

	kdataold, err := ms.ReadAll("temp.key")
	assert.NoError(t, err)
	assert.NotEmpty(t, kdataold)

	mender.ForceBootstrap()

	assert.True(t, mender.needsBootstrap())

	merr = mender.Bootstrap()
	assert.NoError(t, merr)

	// bootstrap should have generated a new key
	kdatanew, err := ms.ReadAll("temp.key")
	assert.NoError(t, err)
	assert.NotEmpty(t, kdatanew)
	// we should have a new key
	assert.NotEqual(t, kdatanew, kdataold)
}

func Test_Bootstrap(t *testing.T) {
	mender := newTestMender(nil,
		menderConfig{
			DeviceKey: "temp.key",
		},
		MenderPieces{},
	)

	assert.True(t, mender.needsBootstrap())

	assert.NoError(t, mender.Bootstrap())

	mam, _ := mender.authMgr.(*MenderAuthManager)
	k := NewKeystore(mam.store)
	assert.NotNil(t, k)
	assert.NoError(t, k.Load("temp.key"))
}

func Test_BootstrappedHaveKeys(t *testing.T) {

	// generate valid keys
	ms := NewMemStore()
	k := NewKeystore(ms)
	assert.NotNil(t, k)
	assert.NoError(t, k.Generate())
	assert.NoError(t, k.Save("temp.key"))

	mender := newTestMender(nil,
		menderConfig{
			DeviceKey: "temp.key",
		},
		MenderPieces{
			store: ms,
		},
	)
	assert.NotNil(t, mender)
	mam, _ := mender.authMgr.(*MenderAuthManager)
	assert.Equal(t, ms, mam.keyStore.store)
	assert.NotNil(t, mam.keyStore.private)

	// subsequen bootstrap should not fail
	assert.NoError(t, mender.Bootstrap())
}

func Test_BootstrapError(t *testing.T) {

	ms := NewMemStore()

	ms.Disable(true)

	var mender *mender
	mender = newTestMender(nil, menderConfig{}, MenderPieces{
		store: ms,
	})
	// store is disabled, attempts to load keys when creating authMgr should have
	// failed
	assert.Nil(t, mender.authMgr)

	ms.Disable(false)
	mender = newTestMender(nil, menderConfig{}, MenderPieces{
		store: ms,
	})
	assert.NotNil(t, mender.authMgr)

	ms.ReadOnly(true)

	err := mender.Bootstrap()
	assert.Error(t, err)
}

func Test_CheckUpdateSimple(t *testing.T) {

	var mender *mender

	mender = newTestMender(nil, menderConfig{}, MenderPieces{
		updater: &fakeUpdater{
			GetScheduledUpdateReturnError: errors.New("check failed"),
		},
	})
	up, err := mender.CheckUpdate()
	assert.Error(t, err)
	assert.Nil(t, up)

	update := UpdateResponse{}
	updaterIface := &fakeUpdater{
		GetScheduledUpdateReturnIface: update,
	}
	mender = newTestMender(nil, menderConfig{}, MenderPieces{
		updater: updaterIface,
	})

	currID := mender.GetCurrentImageID()
	// make image ID same as current, will result in no updates being available
	update.Image.YoctoID = currID
	updaterIface.GetScheduledUpdateReturnIface = update
	up, err = mender.CheckUpdate()
	assert.NoError(t, err)
	assert.Nil(t, up)

	// make image ID different from current
	update.Image.YoctoID = currID + "-fake"
	updaterIface.GetScheduledUpdateReturnIface = update
	up, err = mender.CheckUpdate()
	assert.NoError(t, err)
	assert.NotNil(t, up)
	assert.Equal(t, &update, up)
}

func TestMenderHasUpgrade(t *testing.T) {
	runner := newTestOSCalls("upgrade_available=1", 0)
	mender := newTestMender(&runner, menderConfig{}, MenderPieces{})

	h, err := mender.HasUpgrade()
	assert.NoError(t, err)
	assert.True(t, h)

	runner = newTestOSCalls("upgrade_available=0", 0)
	mender = newTestMender(&runner, menderConfig{}, MenderPieces{})

	h, err = mender.HasUpgrade()
	assert.NoError(t, err)
	assert.False(t, h)

	runner = newTestOSCalls("", -1)
	mender = newTestMender(&runner, menderConfig{}, MenderPieces{})
	h, err = mender.HasUpgrade()
	assert.Error(t, err)
}

func TestMenderGetPollInterval(t *testing.T) {
	mender := newTestMender(nil, menderConfig{
		PollIntervalSeconds: 20,
	}, MenderPieces{})

	intvl := mender.GetUpdatePollInterval()
	assert.Equal(t, time.Duration(20)*time.Second, intvl)
}

type testAuthManager struct {
	authorized     bool
	authcode       AuthCode
	authcodeErr    error
	haskey         bool
	generatekeyErr error
	testAuthDataMessenger
}

func (a *testAuthManager) IsAuthorized() bool {
	return a.authorized
}

func (a *testAuthManager) AuthCode() (AuthCode, error) {
	return a.authcode, a.authcodeErr
}

func (a *testAuthManager) HasKey() bool {
	return a.haskey
}

func (a *testAuthManager) GenerateKey() error {
	return a.generatekeyErr
}

func TestMenderAuthorize(t *testing.T) {
	runner := newTestOSCalls("", -1)

	rspdata := []byte("foobar")

	authReq := &fakeAuthorizer{
		rsp: rspdata,
	}
	authMgr := &testAuthManager{
		authorized: true,
	}

	mender := newTestMender(&runner,
		menderConfig{
			ServerURL: "localhost:2323",
		},
		MenderPieces{
			authMgr: authMgr,
			authReq: authReq,
		})

	err := mender.Authorize()
	assert.NoError(t, err)
	// no need to build send request if auth data is valid
	assert.False(t, authReq.reqCalled)

	authReq.rspErr = errors.New("request error")
	authMgr.authorized = false
	err = mender.Authorize()
	assert.Error(t, err)
	assert.False(t, err.IsFatal())
	assert.True(t, authReq.reqCalled)
	assert.Equal(t, "localhost:2323", authReq.url)

	// clear error
	authReq.rspErr = nil
	authMgr.testAuthDataMessenger.rspError = errors.New("response parse error")
	err = mender.Authorize()
	assert.Error(t, err)
	assert.False(t, err.IsFatal())
	// response data should be passed verbatim to AuthDataMessenger interface
	assert.Equal(t, rspdata, authMgr.testAuthDataMessenger.rspData)

	authMgr.testAuthDataMessenger.rspError = nil
	err = mender.Authorize()
	assert.NoError(t, err)
}
