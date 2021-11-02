import React, { FC, useEffect } from 'react';
import { getNavModel } from 'app/core/selectors/navModel';
import Page from 'app/core/components/Page/Page';
import { useSelector } from 'react-redux';
import { StoreState } from 'app/types/store';
import { LinkButton } from '@grafana/ui';
import { getBackendSrv } from '@grafana/runtime';
import { AdminOrgsTable } from './AdminOrgsTable';
import useAsyncFn from 'react-use/lib/useAsyncFn';
import { contextSrv } from 'app/core/services/context_srv';
import { AccessControlAction } from 'app/types';

const deleteOrg = async (orgId: number) => {
  if (contextSrv.hasPermission(AccessControlAction.OrgsDelete)) {
    return await getBackendSrv().delete('/api/orgs/' + orgId);
  }
  return {};
};

const getOrgs = async () => {
  if (contextSrv.hasPermission(AccessControlAction.OrgsRead)) {
    return await getBackendSrv().get('/api/orgs');
  }
  return [];
};

export const AdminListOrgsPages: FC = () => {
  const navIndex = useSelector((state: StoreState) => state.navIndex);
  const navModel = getNavModel(navIndex, 'global-orgs');
  const [state, fetchOrgs] = useAsyncFn(async () => await getOrgs(), []);
  const isOrgCreator = contextSrv.hasPermission(AccessControlAction.OrgsCreate);

  useEffect(() => {
    fetchOrgs();
  }, [fetchOrgs]);

  return (
    <Page navModel={navModel}>
      <Page.Contents>
        <>
          <div className="page-action-bar">
            <div className="page-action-bar__spacer" />
            <LinkButton icon="plus" href="org/new" disabled={!isOrgCreator}>
              New org
            </LinkButton>
          </div>
          {state.loading && 'Fetching organizations'}
          {state.error}
          {state.value && (
            <AdminOrgsTable
              orgs={state.value}
              onDelete={(orgId) => {
                deleteOrg(orgId).then(() => fetchOrgs());
              }}
            />
          )}
        </>
      </Page.Contents>
    </Page>
  );
};

export default AdminListOrgsPages;
