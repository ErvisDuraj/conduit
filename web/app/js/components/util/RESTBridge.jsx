import _ from 'lodash';
import React from 'react';


const withREST = (WrappedComponent, request) => {
  return class extends React.Component {
    constructor(props) {
      super(props);
      this.api = this.props.api;

      this.state = this.getInitialState(this.props);
    }

    getInitialState = () => ({
      pollingInterval: 2000, // TODO: poll based on metricsWindow size
      metrics: [],
      pendingRequests: false,
      loading: true,
      error: ''
    });

    componentDidMount() {
      this.startServerPolling(this.props.resource);
    }

    componentWillReceiveProps(newProps) {
      // React won't unmount this component when switching resource pages so we need to clear state
      this.stopServerPolling();
      this.setState(this.getInitialState(newProps));
      this.startServerPolling(newProps.resource);
    }

    componentWillUnmount() {
      this.stopServerPolling();
    }

    startServerPolling = () => {
      this.loadFromServer();
      this.timerId = window.setInterval(
        this.loadFromServer, this.state.pollingInterval);
    }

    stopServerPolling = () => {
      window.clearInterval(this.timerId);
      this.api.cancelCurrentRequests();
    }

    loadFromServer = () => {
      if (this.state.pendingRequests) {
        return; // don't make more requests if the ones we sent haven't completed
      }
      this.setState({ pendingRequests: true });

      this.api.setCurrentRequests([request(this.props)]);

      Promise.all(this.api.getCurrentPromises())
        .then(([data]) => {
          this.setState({
            data: data,
            loading: false,
            pendingRequests: false,
            error: '',
          });
        })
        .catch(this.handleApiError);
    }

    handleApiError = e => {
      if (e.isCanceled) return;

      this.setState({
        pendingRequests: false,
        error: `Error getting data from server: ${e.message}`
      });
    }

    render() {
      return (
        <WrappedComponent
          {..._.pick(this.state, ['data', 'error', 'loading'])}
          {...this.props} />
      );
    }
  };
};

export default withREST;
