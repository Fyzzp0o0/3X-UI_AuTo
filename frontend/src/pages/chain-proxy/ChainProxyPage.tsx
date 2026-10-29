import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, ConfigProvider, Layout, Space, Tabs, Tag, Typography, message } from 'antd';
import {
  ApartmentOutlined,
  ClusterOutlined,
  DashboardOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';

import AppSidebar from '@/layouts/AppSidebar';
import { useTheme } from '@/hooks/useTheme';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { setMessageInstance } from '@/utils/messageBus';
import { useChainMeta } from '@/api/queries/useChain';

import OverviewTab from './sections/OverviewTab';
import PoolsTab from './sections/PoolsTab';
import NodesTab from './sections/NodesTab';
import JobsTab from './sections/JobsTab';
import './ChainProxyPage.css';

type TabKey = 'overview' | 'pools' | 'nodes' | 'jobs';

export default function ChainProxyPage() {
  const { t } = useTranslation();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const { isMobile } = useMediaQuery();
  const [messageApi, messageContextHolder] = message.useMessage();
  const [tab, setTab] = useState<TabKey>('overview');
  const [jobId, setJobId] = useState<number | null>(null);
  const { data: meta } = useChainMeta();

  useEffect(() => {
    setMessageInstance(messageApi);
  }, [messageApi]);

  const onJobStarted = useCallback((id: number) => {
    setJobId(id);
    setTab('jobs');
  }, []);

  const pageClass = ['chain-proxy-page', isDark ? 'is-dark' : '', isUltra ? 'is-ultra' : '']
    .filter(Boolean)
    .join(' ');

  const items = [
    {
      key: 'overview',
      label: (
        <span>
          <DashboardOutlined aria-hidden /> {t('pages.chainProxy.tabs.overview')}
        </span>
      ),
      children: <OverviewTab onOpenPools={() => setTab('pools')} />,
    },
    {
      key: 'pools',
      label: (
        <span>
          <ClusterOutlined aria-hidden /> {t('pages.chainProxy.tabs.pools')}
        </span>
      ),
      children: <PoolsTab meta={meta} onJobStarted={onJobStarted} />,
    },
    {
      key: 'nodes',
      label: (
        <span>
          <ApartmentOutlined aria-hidden /> {t('pages.chainProxy.tabs.nodes')}
        </span>
      ),
      children: <NodesTab />,
    },
    {
      key: 'jobs',
      label: (
        <span>
          <ThunderboltOutlined aria-hidden /> {t('pages.chainProxy.tabs.jobs')}
        </span>
      ),
      children: <JobsTab highlightJobId={jobId} />,
    },
  ];

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      <Layout className={pageClass}>
        <AppSidebar />
        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <Space direction="vertical" size={isMobile ? 8 : 16} style={{ width: '100%' }}>
              <div className="chain-proxy-header">
                <Typography.Title level={4} style={{ margin: 0 }}>
                  {t('pages.chainProxy.title')}
                </Typography.Title>
                <Space size={4} wrap>
                  {meta ? (
                    <Tag color={meta.enabled ? 'green' : 'default'}>
                      {meta.enabled
                        ? t('pages.chainProxy.badges.enabled')
                        : t('pages.chainProxy.badges.disabled')}
                    </Tag>
                  ) : null}
                  {meta && !meta.encryption ? (
                    <Tag color="orange">{t('pages.chainProxy.badges.noEncryption')}</Tag>
                  ) : null}
                </Space>
              </div>
              {meta && !meta.enabled ? (
                <Alert type="warning" showIcon message={t('pages.chainProxy.hints.disabled')} />
              ) : null}
              <Tabs
                activeKey={tab}
                onChange={(key) => setTab(key as TabKey)}
                items={items}
                destroyOnHidden
              />
            </Space>
          </Layout.Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}
