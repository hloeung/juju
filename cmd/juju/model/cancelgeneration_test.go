// Copyright 2018 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package model_test

import (
	"github.com/golang/mock/gomock"
	"github.com/juju/cmd"
	"github.com/juju/cmd/cmdtesting"
	"github.com/juju/errors"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/cmd/juju/model"
	"github.com/juju/juju/cmd/juju/model/mocks"
	coremodel "github.com/juju/juju/core/model"
)

type cancelGenerationSuite struct {
	generationBaseSuite
}

var _ = gc.Suite(&cancelGenerationSuite{})

func (s *cancelGenerationSuite) TestInit(c *gc.C) {
	err := s.runInit(s.branchName)
	c.Assert(err, jc.ErrorIsNil)
}

func (s *cancelGenerationSuite) TestInitFail(c *gc.C) {
	err := s.runInit()
	c.Assert(err, gc.ErrorMatches, "must specify a branch name to commit")
}

func (s *cancelGenerationSuite) TestRunCommand(c *gc.C) {
	ctrl, api := setUpCancelMocks(c)
	defer ctrl.Finish()

	api.EXPECT().CommitBranch(gomock.Any(), s.branchName).Return(3, nil)

	ctx, err := s.runCommand(c, api)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(cmdtesting.Stdout(ctx), gc.Equals, `
changes committed; model is now at generation 3
active branch set to "master"`[1:])

	// Ensure the local store has "master" as the target.
	details, err := s.store.ModelByName(s.store.CurrentControllerName, s.store.Models[s.store.CurrentControllerName].CurrentModel)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(details.ModelGeneration, gc.Equals, coremodel.GenerationMaster)
}

func (s *cancelGenerationSuite) TestRunCommandFail(c *gc.C) {
	ctrl, api := setUpCancelMocks(c)
	defer ctrl.Finish()

	api.EXPECT().CommitBranch(gomock.Any(), s.branchName).Return(0, errors.Errorf("fail"))

	_, err := s.runCommand(c, api)
	c.Assert(err, gc.ErrorMatches, "fail")
}

func (s *cancelGenerationSuite) runInit(args ...string) error {
	return cmdtesting.InitCommand(model.NewCancelGenerationCommandForTest(nil, s.store), args)
}

func (s *cancelGenerationSuite) runCommand(c *gc.C, api model.CancelGenerationCommandAPI) (*cmd.Context, error) {
	return cmdtesting.RunCommand(c, model.NewCancelGenerationCommandForTest(api, s.store), s.branchName)
}

func setUpCancelMocks(c *gc.C) (*gomock.Controller, *mocks.MockCancelGenerationCommandAPI) {
	ctrl := gomock.NewController(c)
	api := mocks.NewMockCancelGenerationCommandAPI(ctrl)
	api.EXPECT().Close()
	return ctrl, api
}