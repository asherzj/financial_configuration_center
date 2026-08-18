import { App as AntApp, Button, Card, Flex, Layout, Space, Tag, Typography } from "antd";
import { Link, Outlet } from "react-router";

const { Header, Content, Sider } = Layout;

export function App() {
  return (
    <AntApp>
      <Layout className="app-shell">
        <Sider width={240} theme="light" className="app-sidebar">
          <Typography.Title level={3} className="app-logo">
            FinConfig
          </Typography.Title>
          <nav aria-label="主导航">
            <Space orientation="vertical" size={4} className="app-navigation">
              <Link to="/">仪表盘</Link>
              <Link to="/operations">统一操作入口</Link>
              <Link to="/releases">发布单</Link>
              <Link to="/diagnostics">运行诊断</Link>
            </Space>
          </nav>
        </Sider>
        <Layout>
          <Header className="app-header">
            <Flex justify="space-between" align="center">
              <Space>
                <Tag color="blue">Region: cn</Tag>
                <Tag color="gold">Environment: staging</Tag>
              </Space>
              <Button type="text">设计基线</Button>
            </Flex>
          </Header>
          <Content className="app-content">
            <Outlet />
          </Content>
        </Layout>
      </Layout>
    </AntApp>
  );
}

export function DashboardPage() {
  return (
    <Card>
      <Typography.Title level={1}>金融配置中心</Typography.Title>
      <Typography.Paragraph>
        通过受控发布管理结构化配置，并为 Go 服务提供 last-known-good 读取能力。
      </Typography.Paragraph>
    </Card>
  );
}

export function PlaceholderPage({ title }: { title: string }) {
  return (
    <Card>
      <Typography.Title level={1}>{title}</Typography.Title>
      <Typography.Paragraph>该页面将在对应垂直切片中接入真实契约。</Typography.Paragraph>
    </Card>
  );
}
